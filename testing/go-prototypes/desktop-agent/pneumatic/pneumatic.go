package pneumatic

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ValveType represents different pneumatic valve configurations
type ValveType string

const (
	// SingleSolenoid - 5/2 single solenoid valve
	SingleSolenoid ValveType = "single_solenoid"
	// DoubleSolenoid - 5/2 double solenoid valve
	DoubleSolenoid ValveType = "double_solenoid"
	// ThreePort - 3/2 normally closed valve
	ThreePort ValveType = "three_port"
)

// ValveState represents valve position
type ValveState string

const (
	// ValveClosed - valve is closed/vented
	ValveClosed ValveState = "closed"
	// ValveOpen - valve is open/actuated
	ValveOpen ValveState = "open"
	// ValveCenter - center position (for double solenoid, both sides equalized)
	ValveCenter ValveState = "center"
)

// Valve represents a pneumatic valve control interface
type Valve interface {
	// Type returns the valve type
	Type() ValveType
	// Channel returns the valve channel/ID
	Channel() int
	// SetPosition sets the valve position
	SetPosition(ctx context.Context, state ValveState) error
	// GetPosition returns current valve position
	GetPosition(ctx context.Context) (ValveState, error)
	// Close vents the valve to atmosphere
	Close(ctx context.Context) error
}

// SolenoidValve controls solenoid pneumatic valves
type SolenoidValve struct {
	channel        int
	pinA           int  // control pin A (or single for single solenoid)
	pinB           int  // control pin B (for double solenoid)
	normalOpen     bool // true if normally open
	currentState   ValveState
	transitionTime time.Duration // time to complete valve transition
	mu             sync.Mutex
}

// NewSolenoidValve creates a new solenoid valve controller
func NewSolenoidValve(channel, pinA int) *SolenoidValve {
	return &SolenoidValve{
		channel:        channel,
		pinA:           pinA,
		pinB:           -1,
		normalOpen:     false,
		currentState:   ValveClosed,
		transitionTime: 50 * time.Millisecond,
	}
}

// NewDoubleSolenoidValve creates a double solenoid valve controller
func NewDoubleSolenoidValve(channel, pinA, pinB int) *SolenoidValve {
	return &SolenoidValve{
		channel:        channel,
		pinA:           pinA,
		pinB:           pinB,
		normalOpen:     false,
		currentState:   ValveClosed,
		transitionTime: 50 * time.Millisecond,
	}
}

func (v *SolenoidValve) Type() ValveType {
	if v.pinB >= 0 {
		return DoubleSolenoid
	}
	return SingleSolenoid
}
func (v *SolenoidValve) Channel() int { return v.channel }

func (v *SolenoidValve) SetPosition(ctx context.Context, state ValveState) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	var pinA, pinB int

	switch v.Type() {
	case SingleSolenoid:
		switch state {
		case ValveOpen:
			pinA = 1 // energize
		case ValveClosed:
			pinA = 0 // de-energize
		default:
			return fmt.Errorf("unsupported state %s for single solenoid valve", state)
		}
	case DoubleSolenoid:
		switch state {
		case ValveOpen:
			pinA, pinB = 1, 0 // side A pressurized
		case ValveClosed:
			pinA, pinB = 0, 1 // side B pressurized
		case ValveCenter:
			pinA, pinB = 0, 0 // both sides equalized
		default:
			return fmt.Errorf("unsupported state %s for double solenoid valve", state)
		}
	default:
		return fmt.Errorf("unsupported valve type: %s", v.Type())
	}

	// Set pins
	if err := v.setPin(ctx, v.pinA, pinA); err != nil {
		return fmt.Errorf("pin A: %w", err)
	}

	if v.pinB >= 0 {
		if err := v.setPin(ctx, v.pinB, pinB); err != nil {
			return fmt.Errorf("pin B: %w", err)
		}
	}

	// Wait for valve transition
	select {
	case <-time.After(v.transitionTime):
		v.currentState = state
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (v *SolenoidValve) GetPosition(ctx context.Context) (ValveState, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.currentState, nil
}

func (v *SolenoidValve) Close(ctx context.Context) error {
	if v.normalOpen {
		// Normally open: de-energize to close
		return v.SetPosition(ctx, ValveClosed)
	}
	// Normally closed: already closed when de-energized
	return v.SetPosition(ctx, ValveClosed)
}

func (v *SolenoidValve) setPin(ctx context.Context, pin, value int) error {
	// M42 P<pin> S<value>
	// This is a placeholder - actual implementation depends on hardware
	// For Yaver Box: command to GPIO controller
	return nil
}

// Cylinder represents a pneumatic cylinder with position feedback
type Cylinder struct {
	channel          int
	name             string
	stroke           float64 // stroke length in mm
	homePosition     float64 // position at home (fully retracted)
	extendedPosition float64 // position when extended
	retractValve     int     // valve channel for retract
	extendValve      int     // valve channel for extend
	hasFeedback      bool    // has position sensor
	position         float64 // current position
	isExtended       bool    // current state
	mu               sync.Mutex
}

// NewCylinder creates a new pneumatic cylinder controller
func NewCylinder(channel int, name string, stroke, homePos, extendPos float64, retractValve, extendValve int, hasFeedback bool) *Cylinder {
	return &Cylinder{
		channel:          channel,
		name:             name,
		stroke:           stroke,
		homePosition:     homePos,
		extendedPosition: extendPos,
		retractValve:     retractValve,
		extendValve:      extendValve,
		hasFeedback:      hasFeedback,
		position:         homePos,
		isExtended:       false,
	}
}

func (c *Cylinder) Type() string { return "cylinder" }
func (c *Cylinder) Channel() int { return c.channel }

// Extend extends the cylinder (actuates extend valve)
func (c *Cylinder) Extend(ctx context.Context, valves map[int]Valve) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	extendV, ok := valves[c.extendValve]
	if !ok {
		return fmt.Errorf("extend valve %d not found", c.extendValve)
	}

	retractV, ok := valves[c.retractValve]
	if !ok {
		return fmt.Errorf("retract valve %d not found", c.retractValve)
	}

	// Close retract valve first to prevent backflow
	if err := retractV.Close(ctx); err != nil {
		return fmt.Errorf("close retract valve: %w", err)
	}

	// Open extend valve
	if err := extendV.SetPosition(ctx, ValveOpen); err != nil {
		return fmt.Errorf("open extend valve: %w", err)
	}

	c.isExtended = true
	c.position = c.extendedPosition
	return nil
}

// Retract retracts the cylinder (actuates retract valve)
func (c *Cylinder) Retract(ctx context.Context, valves map[int]Valve) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	extendV, ok := valves[c.extendValve]
	if !ok {
		return fmt.Errorf("extend valve %d not found", c.extendValve)
	}

	retractV, ok := valves[c.retractValve]
	if !ok {
		return fmt.Errorf("retract valve %d not found", c.retractValve)
	}

	// Close extend valve first to prevent backflow
	if err := extendV.Close(ctx); err != nil {
		return fmt.Errorf("close extend valve: %w", err)
	}

	// Open retract valve
	if err := retractV.SetPosition(ctx, ValveOpen); err != nil {
		return fmt.Errorf("open retract valve: %w", err)
	}

	c.isExtended = false
	c.position = c.homePosition
	return nil
}

// GetPosition returns current cylinder position
func (c *Cylinder) GetPosition(ctx context.Context) (float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.hasFeedback {
		// No feedback sensor, return logical position
		if c.isExtended {
			return c.extendedPosition, nil
		}
		return c.homePosition, nil
	}

	// Read position from sensor (placeholder)
	// For Yaver Box: read from analog or digital position sensor
	return c.position, nil
}

// IsExtended returns whether cylinder is currently extended
func (c *Cylinder) IsExtended() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.isExtended
}

// PressureSensor reads pressure from a pneumatic pressure sensor
type PressureSensor struct {
	channel      int
	pin          int     // analog input pin for sensor
	minPressure  float64 // minimum pressure (kPa/bar)
	maxPressure  float64 // maximum pressure (kPa/bar)
	unit         string  // pressure unit (kPa, bar, PSI)
	lastPressure float64
	lastReading  time.Time
	mu           sync.Mutex
}

// NewPressureSensor creates a new pressure sensor controller
func NewPressureSensor(channel, pin int, minP, maxP float64, unit string) *PressureSensor {
	if unit == "" {
		unit = "bar" // default unit
	}
	return &PressureSensor{
		channel:      channel,
		pin:          pin,
		minPressure:  minP,
		maxPressure:  maxP,
		unit:         unit,
		lastPressure: 0,
		lastReading:  time.Now(),
	}
}

func (p *PressureSensor) Type() string { return "pressure_sensor" }
func (p *PressureSensor) Channel() int { return p.channel }

// Read reads current pressure from sensor
func (p *PressureSensor) Read(ctx context.Context) (float64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Read analog value from pin (placeholder)
	// For Yaver Box: read from ADC via I2C/SPI controller
	// Convert voltage to pressure based on sensor range
	rawValue := 0.0 // placeholder

	// Map raw value to pressure range
	pressure := p.minPressure + (p.maxPressure-p.minPressure)*rawValue

	p.lastPressure = pressure
	p.lastReading = time.Now()
	return pressure, nil
}

// GetLastReading returns the last pressure reading without re-reading sensor
func (p *PressureSensor) GetLastReading() (float64, time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastPressure, p.lastReading
}

// Manager manages all pneumatic components
type Manager struct {
	valves          map[int]Valve
	cylinders       map[int]*Cylinder
	pressureSensors map[int]*PressureSensor
	mu              sync.Mutex
}

// NewManager creates a new pneumatic manager
func NewManager() *Manager {
	return &Manager{
		valves:          make(map[int]Valve),
		cylinders:       make(map[int]*Cylinder),
		pressureSensors: make(map[int]*PressureSensor),
	}
}

// AddValve adds a valve to the manager
func (m *Manager) AddValve(v Valve) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.valves[v.Channel()] = v
}

// AddCylinder adds a cylinder to the manager
func (m *Manager) AddCylinder(c *Cylinder) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cylinders[c.channel] = c
}

// AddPressureSensor adds a pressure sensor to the manager
func (m *Manager) AddPressureSensor(p *PressureSensor) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pressureSensors[p.channel] = p
}

// GetValve retrieves a valve by channel
func (m *Manager) GetValve(channel int) (Valve, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.valves[channel]
	if !ok {
		return nil, fmt.Errorf("valve %d not found", channel)
	}
	return v, nil
}

// GetCylinder retrieves a cylinder by channel
func (m *Manager) GetCylinder(channel int) (*Cylinder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.cylinders[channel]
	if !ok {
		return nil, fmt.Errorf("cylinder %d not found", channel)
	}
	return c, nil
}

// GetPressureSensor retrieves a pressure sensor by channel
func (m *Manager) GetPressureSensor(channel int) (*PressureSensor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pressureSensors[channel]
	if !ok {
		return nil, fmt.Errorf("pressure sensor %d not found", channel)
	}
	return p, nil
}

// ValveControl controls a valve
func (m *Manager) ValveControl(ctx context.Context, channel int, state ValveState) error {
	v, err := m.GetValve(channel)
	if err != nil {
		return err
	}
	return v.SetPosition(ctx, state)
}

// CylinderExtend extends a cylinder
func (m *Manager) CylinderExtend(ctx context.Context, channel int) error {
	c, err := m.GetCylinder(channel)
	if err != nil {
		return err
	}
	return c.Extend(ctx, m.valves)
}

// CylinderRetract retracts a cylinder
func (m *Manager) CylinderRetract(ctx context.Context, channel int) error {
	c, err := m.GetCylinder(channel)
	if err != nil {
		return err
	}
	return c.Retract(ctx, m.valves)
}

// ReadPressure reads pressure from a sensor
func (m *Manager) ReadPressure(ctx context.Context, channel int) (float64, error) {
	p, err := m.GetPressureSensor(channel)
	if err != nil {
		return 0, err
	}
	return p.Read(ctx)
}

// GetPressure reads pressure from a sensor (alias for ReadPressure)
func (m *Manager) GetPressure(ctx context.Context, channel int) (float64, error) {
	return m.ReadPressure(ctx, channel)
}

// EmergencyStop performs emergency stop on all pneumatics (close all valves)
func (m *Manager) EmergencyStop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	for _, v := range m.valves {
		if err := v.Close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("valve %d: %w", v.Channel(), err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("some valves failed to close: %v", errs)
	}
	return nil
}

// SafeMode sets all cylinders to safe state (retracted)
func (m *Manager) SafeMode(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	for _, c := range m.cylinders {
		if c.IsExtended() {
			if err := c.Retract(ctx, m.valves); err != nil {
				errs = append(errs, fmt.Errorf("cylinder %d: %w", c.channel, err))
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("some cylinders failed to retract: %v", errs)
	}
	return nil
}

// ListValves returns all valve channels
func (m *Manager) ListValves() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	channels := make([]int, 0, len(m.valves))
	for ch := range m.valves {
		channels = append(channels, ch)
	}
	return channels
}

// ListCylinders returns all cylinder channels
func (m *Manager) ListCylinders() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	channels := make([]int, 0, len(m.cylinders))
	for ch := range m.cylinders {
		channels = append(channels, ch)
	}
	return channels
}

// ListPressureSensors returns all pressure sensor channels
func (m *Manager) ListPressureSensors() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	channels := make([]int, 0, len(m.pressureSensors))
	for ch := range m.pressureSensors {
		channels = append(channels, ch)
	}
	return channels
}

// Status returns status of all pneumatic components
type PneumaticStatus struct {
	Valves          []ValveStatus          `json:"valves"`
	Cylinders       []CylinderStatus       `json:"cylinders"`
	PressureSensors []PressureSensorStatus `json:"pressureSensors"`
}

type ValveStatus struct {
	Channel int        `json:"channel"`
	Type    string     `json:"type"`
	State   ValveState `json:"state"`
	PinA    int        `json:"pinA"`
	PinB    int        `json:"pinB,omitempty"`
}

type CylinderStatus struct {
	Channel     int     `json:"channel"`
	Name        string  `json:"name"`
	Stroke      float64 `json:"stroke"`
	Position    float64 `json:"position"`
	Extended    bool    `json:"extended"`
	HasFeedback bool    `json:"hasFeedback"`
}

type PressureSensorStatus struct {
	Channel  int       `json:"channel"`
	Unit     string    `json:"unit"`
	Pressure float64   `json:"pressure"`
	LastRead time.Time `json:"lastRead"`
}

func (m *Manager) Status(ctx context.Context) (PneumaticStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	status := PneumaticStatus{
		Valves:          make([]ValveStatus, 0, len(m.valves)),
		Cylinders:       make([]CylinderStatus, 0, len(m.cylinders)),
		PressureSensors: make([]PressureSensorStatus, 0, len(m.pressureSensors)),
	}

	// Valve status
	for _, v := range m.valves {
		state, _ := v.GetPosition(ctx)
		vs := ValveStatus{
			Channel: v.Channel(),
			Type:    string(v.Type()),
			State:   state,
			PinA:    0, // placeholder - would read from valve
		}
		if sv, ok := v.(*SolenoidValve); ok {
			vs.PinA = sv.pinA
			vs.PinB = sv.pinB
		}
		status.Valves = append(status.Valves, vs)
	}

	// Cylinder status
	for _, c := range m.cylinders {
		pos, _ := c.GetPosition(ctx)
		status.Cylinders = append(status.Cylinders, CylinderStatus{
			Channel:     c.channel,
			Name:        c.name,
			Stroke:      c.stroke,
			Position:    pos,
			Extended:    c.isExtended,
			HasFeedback: c.hasFeedback,
		})
	}

	// Pressure sensor status
	for _, p := range m.pressureSensors {
		pressure, lastRead := p.GetLastReading()
		status.PressureSensors = append(status.PressureSensors, PressureSensorStatus{
			Channel:  p.channel,
			Unit:     p.unit,
			Pressure: pressure,
			LastRead: lastRead,
		})
	}

	return status, nil
}

// Config represents pneumatic component configuration
type ValveConfig struct {
	Channel    int            `json:"channel"`
	Type       ValveType      `json:"type"`
	Pins       map[string]int `json:"pins"`
	NormalOpen bool           `json:"normalOpen,omitempty"`
}

type CylinderConfig struct {
	Channel      int     `json:"channel"`
	Name         string  `json:"name"`
	Stroke       float64 `json:"stroke"`
	HomePos      float64 `json:"homePos"`
	ExtendPos    float64 `json:"extendPos"`
	RetractValve int     `json:"retractValve"`
	ExtendValve  int     `json:"extendValve"`
	HasFeedback  bool    `json:"hasFeedback"`
}

type PressureSensorConfig struct {
	Channel int     `json:"channel"`
	Pin     int     `json:"pin"`
	MinP    float64 `json:"minPressure"`
	MaxP    float64 `json:"maxPressure"`
	Unit    string  `json:"unit"`
}

// CreateValve creates a valve from config
func CreateValve(cfg ValveConfig) (Valve, error) {
	if cfg.Type == SingleSolenoid {
		pinA, ok := cfg.Pins["control"]
		if !ok {
			return nil, fmt.Errorf("control pin required for single solenoid valve")
		}
		v := NewSolenoidValve(cfg.Channel, pinA)
		v.normalOpen = cfg.NormalOpen
		return v, nil
	} else if cfg.Type == DoubleSolenoid {
		pinA, okA := cfg.Pins["controlA"]
		pinB, okB := cfg.Pins["controlB"]
		if !okA || !okB {
			return nil, fmt.Errorf("controlA and controlB pins required for double solenoid valve")
		}
		return NewDoubleSolenoidValve(cfg.Channel, pinA, pinB), nil
	} else if cfg.Type == ThreePort {
		// Three port valves are similar to single solenoid
		pinA, ok := cfg.Pins["control"]
		if !ok {
			return nil, fmt.Errorf("control pin required for three port valve")
		}
		v := NewSolenoidValve(cfg.Channel, pinA)
		v.normalOpen = cfg.NormalOpen
		return v, nil
	}

	return nil, fmt.Errorf("unsupported valve type: %s", cfg.Type)
}

// CreateCylinder creates a cylinder from config
func CreateCylinder(cfg CylinderConfig) (*Cylinder, error) {
	return NewCylinder(
		cfg.Channel,
		cfg.Name,
		cfg.Stroke,
		cfg.HomePos,
		cfg.ExtendPos,
		cfg.RetractValve,
		cfg.ExtendValve,
		cfg.HasFeedback,
	), nil
}

// CreatePressureSensor creates a pressure sensor from config
func CreatePressureSensor(cfg PressureSensorConfig) (*PressureSensor, error) {
	return NewPressureSensor(
		cfg.Channel,
		cfg.Pin,
		cfg.MinP,
		cfg.MaxP,
		cfg.Unit,
	), nil
}

// Pressure unit conversions
func BarToPSI(bar float64) float64 {
	return bar * 14.5037738
}

func PSIToBar(psi float64) float64 {
	return psi / 14.5037738
}

func BarToKPa(bar float64) float64 {
	return bar * 100.0
}

func KPaToBar(kpa float64) float64 {
	return kpa / 100.0
}
