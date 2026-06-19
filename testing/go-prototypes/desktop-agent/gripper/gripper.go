package gripper

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

// GripperType represents different gripper configurations
type GripperType string

const (
	// TwoFinger - Two-finger parallel gripper (most common)
	TwoFinger GripperType = "two_finger"
	// ThreeFinger - Three-finger gripper for more complex grasp
	ThreeFinger GripperType = "three_finger"
	// Suction - Suction/vacuum gripper for flat/smooth objects
	Suction GripperType = "suction"
	// Magnetic - Electromagnetic gripper for ferrous objects
	Magnetic GripperType = "magnetic"
)

// Gripper represents a gripper control interface
type Gripper interface {
	// Type returns the gripper type
	Type() GripperType
	// Channel returns the gripper channel/ID
	Channel() int
	// Open opens the gripper
	Open(ctx context.Context, width float64) error
	// Close closes the gripper
	Close(ctx context.Context, force float64) error
	// GetState returns current gripper state
	GetState(ctx context.Context) (GripperState, error)
	// Release opens gripper fully
	Release(ctx context.Context) error
	// Calibrate performs gripper calibration
	Calibrate(ctx context.Context) error
}

// GripperState represents current gripper status
type GripperState struct {
	IsOpen    bool    `json:"isOpen"`
	IsClosed  bool    `json:"isClosed"`
	Position  float64 `json:"position"`  // finger position (0=closed, 1=open)
	Force     float64 `json:"force"`     // current force (0-1.0)
	Width     float64 `json:"width"`     // gripper width in mm
	SuctionOn bool    `json:"suctionOn"` // for suction grippers
	MagnetOn  bool    `json:"magnetOn"`  // for magnetic grippers
	HasObject bool    `json:"hasObject"` // object detection
}

// TwoFingerGripper controls a two-finger parallel gripper
type TwoFingerGripper struct {
	channel        int
	minWidth       float64 // minimum finger width (mm)
	maxWidth       float64 // maximum finger width (mm)
	currentWidth   float64
	targetForce    float64 // 0.0-1.0
	hasObject      bool
	position       float64 // 0.0=closed, 1.0=open
	transitionTime time.Duration
	mu             sync.Mutex
}

// NewTwoFingerGripper creates a new two-finger gripper controller
func NewTwoFingerGripper(channel int, minWidth, maxWidth float64) *TwoFingerGripper {
	return &TwoFingerGripper{
		channel:        channel,
		minWidth:       minWidth,
		maxWidth:       maxWidth,
		currentWidth:   maxWidth,
		targetForce:    0.5, // default moderate force
		hasObject:      false,
		position:       1.0, // start open
		transitionTime: 500 * time.Millisecond,
	}
}

func (g *TwoFingerGripper) Type() GripperType { return TwoFinger }
func (g *TwoFingerGripper) Channel() int      { return g.channel }

func (g *TwoFingerGripper) Open(ctx context.Context, width float64) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Clamp width
	if width < g.minWidth {
		width = g.minWidth
	}
	if width > g.maxWidth {
		width = g.maxWidth
	}

	// Calculate position from width
	position := (width - g.minWidth) / (g.maxWidth - g.minWidth)
	if position < 0 {
		position = 0
	}
	if position > 1 {
		position = 1
	}

	// Send open command to motor controller
	if err := g.moveTo(ctx, position); err != nil {
		return err
	}

	// Wait for transition
	select {
	case <-time.After(g.transitionTime):
		g.currentWidth = width
		g.position = position
		g.hasObject = false // assume no object after open
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *TwoFingerGripper) Close(ctx context.Context, force float64) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Clamp force
	if force < 0.0 {
		force = 0.0
	}
	if force > 1.0 {
		force = 1.0
	}

	g.targetForce = force

	// For closing, we set force first then move to minimum width
	if err := g.setForce(ctx, force); err != nil {
		return err
	}

	// Move to minimum width (closed)
	if err := g.moveTo(ctx, 0.0); err != nil {
		return err
	}

	// Wait for transition
	select {
	case <-time.After(g.transitionTime):
		g.currentWidth = g.minWidth
		g.position = 0.0
		// Object detection would go here
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *TwoFingerGripper) GetState(ctx context.Context) (GripperState, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	return GripperState{
		IsOpen:    g.position > 0.1,
		IsClosed:  g.position < 0.1,
		Position:  g.position,
		Force:     g.targetForce,
		Width:     g.currentWidth,
		SuctionOn: false,
		MagnetOn:  false,
		HasObject: g.hasObject,
	}, nil
}

func (g *TwoFingerGripper) Release(ctx context.Context) error {
	return g.Open(ctx, g.maxWidth)
}

func (g *TwoFingerGripper) Calibrate(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Calibration sequence: open fully, close fully, measure endpoints
	// For now, this is a placeholder - real implementation would use limit switches
	return nil
}

func (g *TwoFingerGripper) moveTo(ctx context.Context, position float64) error {
	// Send position command to motor controller
	// For Yaver Box: send to servo/stepper controller
	// This is a placeholder - actual implementation depends on hardware
	return nil
}

func (g *TwoFingerGripper) setForce(ctx context.Context, force float64) error {
	// Set force (typically via servo PWM duty cycle)
	// For Yaver Box: send to motor controller
	// This is a placeholder - actual implementation depends on hardware
	return nil
}

// SuctionGripper controls a suction/vacuum gripper
type SuctionGripper struct {
	channel      int
	vacuumOn     bool
	suctionLevel float64 // 0.0-1.0 vacuum strength
	sensorPin    int     // pressure/vacuum sensor pin
	hasObject    bool
	mu           sync.Mutex
}

// NewSuctionGripper creates a new suction gripper controller
func NewSuctionGripper(channel, sensorPin int) *SuctionGripper {
	return &SuctionGripper{
		channel:      channel,
		vacuumOn:     false,
		suctionLevel: 0.8, // default strong suction
		sensorPin:    sensorPin,
		hasObject:    false,
	}
}

func (g *SuctionGripper) Type() GripperType { return Suction }
func (g *SuctionGripper) Channel() int      { return g.channel }

func (g *SuctionGripper) Open(ctx context.Context, width float64) error {
	// For suction grippers, "open" means release suction
	g.mu.Lock()
	defer g.mu.Unlock()

	if err := g.setVacuum(ctx, false); err != nil {
		return err
	}

	select {
	case <-time.After(100 * time.Millisecond):
		g.vacuumOn = false
		g.hasObject = false
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *SuctionGripper) Close(ctx context.Context, force float64) error {
	// For suction grippers, "close" means engage suction
	g.mu.Lock()
	defer g.mu.Unlock()

	if err := g.setVacuum(ctx, true); err != nil {
		return err
	}

	select {
	case <-time.After(500 * time.Millisecond):
		g.vacuumOn = true
		// Check for object presence via pressure sensor
		g.hasObject = g.checkObject(ctx)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *SuctionGripper) GetState(ctx context.Context) (GripperState, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	return GripperState{
		IsOpen:    !g.vacuumOn,
		IsClosed:  g.vacuumOn,
		Position:  0.5, // binary state
		Force:     g.suctionLevel,
		Width:     0, // not applicable
		SuctionOn: g.vacuumOn,
		MagnetOn:  false,
		HasObject: g.hasObject,
	}, nil
}

func (g *SuctionGripper) Release(ctx context.Context) error {
	return g.Open(ctx, 0)
}

func (g *SuctionGripper) Calibrate(ctx context.Context) error {
	// Calibration: test vacuum pump performance, set suction levels
	// For now, this is a placeholder
	return nil
}

func (g *SuctionGripper) setVacuum(ctx context.Context, on bool) error {
	// Turn vacuum pump on/off
	// For Yaver Box: send to relay/motor controller
	// This is a placeholder - actual implementation depends on hardware
	return nil
}

func (g *SuctionGripper) checkObject(ctx context.Context) bool {
	// Read pressure/vacuum sensor to detect object presence
	// For Yaver Box: read from analog sensor pin
	// This is a placeholder - actual implementation depends on hardware
	return false
}

// MagneticGripper controls an electromagnetic gripper
type MagneticGripper struct {
	channel   int
	magnetOn  bool
	strength  float64 // 0.0-1.0 magnetic strength
	hasObject bool
	mu        sync.Mutex
}

// NewMagneticGripper creates a new magnetic gripper controller
func NewMagneticGripper(channel int) *MagneticGripper {
	return &MagneticGripper{
		channel:   channel,
		magnetOn:  false,
		strength:  1.0, // default max strength
		hasObject: false,
	}
}

func (g *MagneticGripper) Type() GripperType { return Magnetic }
func (g *MagneticGripper) Channel() int      { return g.channel }

func (g *MagneticGripper) Open(ctx context.Context, width float64) error {
	// For magnetic grippers, "open" means deactivate electromagnet
	g.mu.Lock()
	defer g.mu.Unlock()

	if err := g.setMagnet(ctx, false); err != nil {
		return err
	}

	select {
	case <-time.After(50 * time.Millisecond):
		g.magnetOn = false
		g.hasObject = false
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *MagneticGripper) Close(ctx context.Context, force float64) error {
	// For magnetic grippers, "close" means activate electromagnet
	g.mu.Lock()
	defer g.mu.Unlock()

	if err := g.setMagnet(ctx, true); err != nil {
		return err
	}

	// Set magnetic strength (via PWM duty cycle)
	g.strength = force
	if err := g.setStrength(ctx, force); err != nil {
		return err
	}

	select {
	case <-time.After(100 * time.Millisecond):
		g.magnetOn = true
		// Object detection would go here (Hall effect sensor)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *MagneticGripper) GetState(ctx context.Context) (GripperState, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	return GripperState{
		IsOpen:    !g.magnetOn,
		IsClosed:  g.magnetOn,
		Position:  0.5, // binary state
		Force:     g.strength,
		Width:     0, // not applicable
		SuctionOn: false,
		MagnetOn:  g.magnetOn,
		HasObject: g.hasObject,
	}, nil
}

func (g *MagneticGripper) Release(ctx context.Context) error {
	return g.Open(ctx, 0)
}

func (g *MagneticGripper) Calibrate(ctx context.Context) error {
	// Calibration: test magnetic strength, calibrate for different loads
	// For now, this is a placeholder
	return nil
}

func (g *MagneticGripper) setMagnet(ctx context.Context, on bool) error {
	// Turn electromagnet on/off
	// For Yaver Box: send to relay/motor controller
	// This is a placeholder - actual implementation depends on hardware
	return nil
}

func (g *MagneticGripper) setStrength(ctx context.Context, strength float64) error {
	// Set magnetic strength via PWM duty cycle
	// For Yaver Box: send to motor controller
	// This is a placeholder - actual implementation depends on hardware
	return nil
}

// ThreeFingerGripper controls a three-finger adaptive gripper
type ThreeFingerGripper struct {
	channel        int
	fingerStates   [3]float64 // 0.0=closed, 1.0=open for each finger
	targetForce    float64    // 0.0-1.0
	currentWidth   float64    // gripper width at fingertips
	minWidth       float64    // minimum width (fully closed)
	maxWidth       float64    // maximum width (fully open)
	hasObject      bool
	transitionTime time.Duration
	mu             sync.Mutex
}

// NewThreeFingerGripper creates a new three-finger gripper controller
func NewThreeFingerGripper(channel int, minWidth, maxWidth float64) *ThreeFingerGripper {
	return &ThreeFingerGripper{
		channel:        channel,
		fingerStates:   [3]float64{1.0, 1.0, 1.0}, // start fully open
		targetForce:    0.5,
		currentWidth:   maxWidth,
		minWidth:       minWidth,
		maxWidth:       maxWidth,
		hasObject:      false,
		transitionTime: 600 * time.Millisecond,
	}
}

func (g *ThreeFingerGripper) Type() GripperType { return ThreeFinger }
func (g *ThreeFingerGripper) Channel() int      { return g.channel }

func (g *ThreeFingerGripper) Open(ctx context.Context, width float64) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Clamp width
	if width < g.minWidth {
		width = g.minWidth
	}
	if width > g.maxWidth {
		width = g.maxWidth
	}

	// Calculate finger positions (all fingers symmetric)
	position := (width - g.minWidth) / (g.maxWidth - g.minWidth)
	if position < 0 {
		position = 0
	}
	if position > 1 {
		position = 1
	}

	// Move all fingers to position
	if err := g.moveFingers(ctx, [3]float64{position, position, position}); err != nil {
		return err
	}

	// Wait for transition
	select {
	case <-time.After(g.transitionTime):
		g.fingerStates = [3]float64{position, position, position}
		g.currentWidth = width
		g.hasObject = false
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *ThreeFingerGripper) Close(ctx context.Context, force float64) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Clamp force
	if force < 0.0 {
		force = 0.0
	}
	if force > 1.0 {
		force = 1.0
	}

	g.targetForce = force

	// Set force first
	if err := g.setForce(ctx, force); err != nil {
		return err
	}

	// Move all fingers to minimum (closed)
	if err := g.moveFingers(ctx, [3]float64{0.0, 0.0, 0.0}); err != nil {
		return err
	}

	// Wait for transition
	select {
	case <-time.After(g.transitionTime):
		g.fingerStates = [3]float64{0.0, 0.0, 0.0}
		g.currentWidth = g.minWidth
		// Object detection would go here
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *ThreeFingerGripper) GetState(ctx context.Context) (GripperState, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Calculate average position
	avgPosition := (g.fingerStates[0] + g.fingerStates[1] + g.fingerStates[2]) / 3.0

	return GripperState{
		IsOpen:    avgPosition > 0.1,
		IsClosed:  avgPosition < 0.1,
		Position:  avgPosition,
		Force:     g.targetForce,
		Width:     g.currentWidth,
		SuctionOn: false,
		MagnetOn:  false,
		HasObject: g.hasObject,
	}, nil
}

func (g *ThreeFingerGripper) Release(ctx context.Context) error {
	return g.Open(ctx, g.maxWidth)
}

func (g *ThreeFingerGripper) Calibrate(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Calibration: calibrate each finger independently
	// For now, this is a placeholder
	return nil
}

func (g *ThreeFingerGripper) moveFingers(ctx context.Context, positions [3]float64) error {
	// Move each finger to target position
	// For Yaver Box: send to motor controller for each finger
	// This is a placeholder - actual implementation depends on hardware
	return nil
}

func (g *ThreeFingerGripper) setForce(ctx context.Context, force float64) error {
	// Set gripping force (via servo PWM or pressure for pneumatic)
	// For Yaver Box: send to motor/pressure controller
	// This is a placeholder - actual implementation depends on hardware
	return nil
}

// Manager manages all gripper controllers
type Manager struct {
	grippers map[int]Gripper
	mu       sync.Mutex
}

// NewManager creates a new gripper manager
func NewManager() *Manager {
	return &Manager{
		grippers: make(map[int]Gripper),
	}
}

// AddGripper adds a gripper to the manager
func (m *Manager) AddGripper(g Gripper) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.grippers[g.Channel()] = g
}

// GetGripper retrieves a gripper by channel
func (m *Manager) GetGripper(channel int) (Gripper, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.grippers[channel]
	if !ok {
		return nil, fmt.Errorf("gripper %d not found", channel)
	}
	return g, nil
}

// ListGrippers returns all gripper channels
func (m *Manager) ListGrippers() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	channels := make([]int, 0, len(m.grippers))
	for ch := range m.grippers {
		channels = append(channels, ch)
	}
	return channels
}

// Open opens a gripper
func (m *Manager) Open(ctx context.Context, channel int, width float64) error {
	g, err := m.GetGripper(channel)
	if err != nil {
		return err
	}
	return g.Open(ctx, width)
}

// Close closes a gripper
func (m *Manager) Close(ctx context.Context, channel int, force float64) error {
	g, err := m.GetGripper(channel)
	if err != nil {
		return err
	}
	return g.Close(ctx, force)
}

// GetState returns gripper state
func (m *Manager) GetState(ctx context.Context, channel int) (GripperState, error) {
	g, err := m.GetGripper(channel)
	if err != nil {
		return GripperState{}, err
	}
	return g.GetState(ctx)
}

// Release releases a gripper
func (m *Manager) Release(ctx context.Context, channel int) error {
	g, err := m.GetGripper(channel)
	if err != nil {
		return err
	}
	return g.Release(ctx)
}

// Calibrate calibrates a gripper
func (m *Manager) Calibrate(ctx context.Context, channel int) error {
	g, err := m.GetGripper(channel)
	if err != nil {
		return err
	}
	return g.Calibrate(ctx)
}

// ReleaseAll releases all grippers
func (m *Manager) ReleaseAll(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	for _, g := range m.grippers {
		if err := g.Release(ctx); err != nil {
			errs = append(errs, fmt.Errorf("channel %d: %w", g.Channel(), err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("some grippers failed to release: %v", errs)
	}
	return nil
}

// EmergencyStop performs emergency stop on all grippers (release all)
func (m *Manager) EmergencyStop(ctx context.Context) error {
	return m.ReleaseAll(ctx)
}

// Status returns status of all grippers
func (m *Manager) Status(ctx context.Context) (map[int]GripperState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	statuses := make(map[int]GripperState, len(m.grippers))
	for _, g := range m.grippers {
		st, err := g.GetState(ctx)
		if err != nil {
			return nil, fmt.Errorf("gripper %d: %w", g.Channel(), err)
		}
		statuses[g.Channel()] = st
	}

	return statuses, nil
}

// Config represents gripper configuration
type GripperConfig struct {
	Channel int                `json:"channel"`
	Type    GripperType        `json:"type"`
	Width   float64            `json:"width,omitempty"` // min/max width for mechanical grippers
	Pins    map[string]int     `json:"pins,omitempty"`
	Options map[string]float64 `json:"options,omitempty"` // type-specific options
}

// CreateGripper creates a gripper from config
func CreateGripper(cfg GripperConfig) (Gripper, error) {
	switch cfg.Type {
	case TwoFinger:
		minWidth := 0.0
		maxWidth := 100.0 // default 100mm
		if cfg.Width > 0 {
			maxWidth = cfg.Width
		}
		if minW, ok := cfg.Options["minWidth"]; ok {
			minWidth = minW
		}
		return NewTwoFingerGripper(cfg.Channel, minWidth, maxWidth), nil

	case ThreeFinger:
		minWidth := 0.0
		maxWidth := 100.0
		if cfg.Width > 0 {
			maxWidth = cfg.Width
		}
		if minW, ok := cfg.Options["minWidth"]; ok {
			minWidth = minW
		}
		return NewThreeFingerGripper(cfg.Channel, minWidth, maxWidth), nil

	case Suction:
		sensorPin := -1
		if p, ok := cfg.Pins["sensor"]; ok {
			sensorPin = p
		}
		return NewSuctionGripper(cfg.Channel, sensorPin), nil

	case Magnetic:
		return NewMagneticGripper(cfg.Channel), nil

	default:
		return nil, fmt.Errorf("unsupported gripper type: %s", cfg.Type)
	}
}

// GripperInfo provides information about a gripper type
type GripperInfo struct {
	Type         string   `json:"type"`
	Description  string   `json:"description"`
	Capabilities []string `json:"capabilities"`
}

// GetInfo returns information about a gripper type
func GetInfo(gType GripperType) GripperInfo {
	switch gType {
	case TwoFinger:
		return GripperInfo{
			Type:         string(TwoFinger),
			Description:  "Two-finger parallel gripper for picking objects",
			Capabilities: []string{"open", "close", "width_control", "force_control", "object_detection"},
		}
	case ThreeFinger:
		return GripperInfo{
			Type:         string(ThreeFinger),
			Description:  "Three-finger adaptive gripper for complex shapes",
			Capabilities: []string{"open", "close", "width_control", "force_control", "finger_independent", "object_detection"},
		}
	case Suction:
		return GripperInfo{
			Type:         string(Suction),
			Description:  "Suction/vacuum gripper for flat/smooth objects",
			Capabilities: []string{"open", "close", "suction_control", "object_detection"},
		}
	case Magnetic:
		return GripperInfo{
			Type:         string(Magnetic),
			Description:  "Electromagnetic gripper for ferrous objects",
			Capabilities: []string{"open", "close", "strength_control", "object_detection"},
		}
	default:
		return GripperInfo{
			Type:         string(gType),
			Description:  "Unknown gripper type",
			Capabilities: []string{},
		}
	}
}

// CalculateRequiredOpening calculates gripper opening needed for an object
func CalculateRequiredOpening(objectWidth, safetyMargin float64) float64 {
	return objectWidth + safetyMargin
}

// EstimateGraspForce estimates required grip force based on object weight and material
func EstimateGraspForce(weightKg, frictionCoeff float64) float64 {
	// F = mg / μ (force = mass * gravity / friction coefficient)
	const gravity = 9.81
	requiredForce := (weightKg * gravity) / frictionCoeff
	// Normalize to 0-1.0 range (assume max grip force can handle 10kg at μ=0.5)
	maxLoad := 10.0 * gravity / 0.5
	return math.Min(1.0, requiredForce/maxLoad)
}
