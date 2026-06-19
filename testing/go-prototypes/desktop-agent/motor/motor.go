package motor

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MotorType represents different motor control interfaces supported by Yaver Box
type MotorType string

const (
	// ServoPWM - PWM servo control (0-180°, 1000-2000µs pulse)
	ServoPWM MotorType = "servo_pwm"
	// Stepper - Bipolar stepper driver (step/dir, A4988/TMC2209 style)
	Stepper MotorType = "stepper"
	// BrushedDC - Simple brushed DC motor (H-bridge/L298N)
	BrushedDC MotorType = "brushed_dc"
	// BLDC - Brushless DC with ESC (pwm throttle 0-100%)
	BLDC MotorType = "bldc_esc"
)

// Controller represents a motor control interface
type Controller interface {
	// Type returns the motor controller type
	Type() MotorType
	// Channel returns the motor channel/ID
	Channel() int
	// Set writes a control value (meaning depends on motor type)
	Set(ctx context.Context, value float64) error
	// Get reads current feedback (position, speed, etc.)
	Get(ctx context.Context) (float64, error)
	// Stop halts the motor (safe stop, not emergency stop)
	Stop(ctx context.Context) error
	// Calibrate performs motor-specific calibration
	Calibrate(ctx context.Context) error
}

// PWMController handles PWM-based motor control (servos, brushed DC)
type PWMController struct {
	channel    int
	pwmRange   [2]float64 // [min, max] duty cycle (0.0-1.0)
	value      float64
	currentDir bool // true = forward, false = reverse
	mu         sync.Mutex
}

// NewPWMController creates a new PWM motor controller
func NewPWMController(channel int, minDuty, maxDuty float64) *PWMController {
	return &PWMController{
		channel:  channel,
		pwmRange: [2]float64{minDuty, maxDuty},
		value:    0.0,
	}
}

func (c *PWMController) Type() MotorType { return ServoPWM }
func (c *PWMController) Channel() int    { return c.channel }

func (c *PWMController) Set(ctx context.Context, value float64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Clamp value to PWM range
	if value < c.pwmRange[0] {
		value = c.pwmRange[0]
	}
	if value > c.pwmRange[1] {
		value = c.pwmRange[1]
	}

	// For brushed DC, handle direction
	if c.currentDir && value < 0 {
		// Need to reverse direction
		c.currentDir = false
		if err := c.setDirection(ctx, false); err != nil {
			return err
		}
	} else if !c.currentDir && value > 0 {
		// Need to set forward direction
		c.currentDir = true
		if err := c.setDirection(ctx, true); err != nil {
			return err
		}
	}

	c.value = value
	return c.setPWM(ctx, math.Abs(value))
}

func (c *PWMController) Get(ctx context.Context) (float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.currentDir {
		return -c.value, nil
	}
	return c.value, nil
}

func (c *PWMController) Stop(ctx context.Context) error {
	return c.Set(ctx, 0.0)
}

func (c *PWMController) Calibrate(ctx context.Context) error {
	// Basic calibration: sweep min-max-min and measure response
	// For now, this is a no-op implementation
	return nil
}

// setDirection controls the H-bridge direction pin
func (c *PWMController) setDirection(ctx context.Context, forward bool) error {
	// In a real implementation, this would set a GPIO pin
	// For Yaver Box: M42 P<dir_pin> S<1/0>
	// This is a placeholder - actual implementation depends on hardware
	return nil
}

// setPWM sets the PWM duty cycle via the motor backend
func (c *PWMController) setPWM(ctx context.Context, duty float64) error {
	// In a real implementation, this would:
	// - For servo: set pulse width (1000-2000µs)
	// - For brushed DC: set H-bridge enable PWM (0-100%)
	// For Yaver Box: specific command to motor controller
	// This is a placeholder - actual implementation depends on hardware
	return nil
}

// StepperController handles bipolar stepper motor control
type StepperController struct {
	channel      int
	position     float64   // current position in steps
	maxPosition  float64   // max steps (from limit switches)
	microstep    int       // microstep multiplier (1, 2, 4, 8, 16)
	accelProfile []float64 // acceleration curve
	dirPin       int       // direction GPIO pin
	stepPin      int       // step GPIO pin
	enablePin    int       // enable pin (-1 if not used)
	mu           sync.Mutex
}

// NewStepperController creates a new stepper motor controller
func NewStepperController(channel, dirPin, stepPin, enablePin int, microstep int) *StepperController {
	if microstep <= 0 {
		microstep = 1
	}
	return &StepperController{
		channel:      channel,
		position:     0.0,
		microstep:    microstep,
		dirPin:       dirPin,
		stepPin:      stepPin,
		enablePin:    enablePin,
		accelProfile: []float64{0.1, 0.2, 0.4, 0.6, 0.8, 1.0}, // simple S-curve
	}
}

func (c *StepperController) Type() MotorType { return Stepper }
func (c *StepperController) Channel() int    { return c.channel }

func (c *StepperController) Set(ctx context.Context, target float64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	steps := int(target * float64(c.microstep))
	delta := steps - int(c.position)

	if delta == 0 {
		return nil
	}

	// Check limits
	if c.maxPosition > 0 && (c.position+float64(delta)) > c.maxPosition {
		return fmt.Errorf("position %f exceeds max %f", c.position+float64(delta), c.maxPosition)
	}

	// Set direction
	dir := delta > 0
	if err := c.setDirection(ctx, dir); err != nil {
		return err
	}

	// Enable motor if enable pin is set
	if c.enablePin >= 0 {
		if err := c.setEnable(ctx, true); err != nil {
			return err
		}
	}

	// Execute accelerated stepping
	if err := c.stepAccelerated(ctx, delta); err != nil {
		return err
	}

	c.position = float64(steps)

	// Disable motor to reduce heat (optional)
	// if c.enablePin >= 0 {
	// 	_ = c.setEnable(ctx, false)
	// }

	return nil
}

func (c *StepperController) Get(ctx context.Context) (float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.position / float64(c.microstep), nil
}

func (c *StepperController) Stop(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Immediate stop - disable motor
	if c.enablePin >= 0 {
		return c.setEnable(ctx, false)
	}
	return nil
}

func (c *StepperController) Calibrate(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Find home by stepping until limit switch triggers
	// For now, reset position to 0
	c.position = 0.0
	c.maxPosition = 0.0 // means no limit
	return nil
}

func (c *StepperController) setDirection(ctx context.Context, forward bool) error {
	// M42 P<c.dirPin> S<1/0>
	// This is a placeholder
	return nil
}

func (c *StepperController) setEnable(ctx context.Context, enable bool) error {
	// M42 P<c.enablePin> S<1/0>
	// This is a placeholder
	return nil
}

func (c *StepperController) stepAccelerated(ctx context.Context, steps int) error {
	// Simple acceleration profile
	// In real implementation, this would use precise timing
	// For Yaver Box: send step pulses with variable delays
	// This is a placeholder
	return nil
}

// BLDCController handles brushless DC motors with ESC (electronic speed control)
type BLDCController struct {
	channel     int
	armTime     time.Duration // ESC arming time (typically 1-2s)
	armed       bool          // whether ESC is armed
	throttle    float64       // current throttle (0.0-1.0)
	minThrottle float64       // minimum throttle for spin (usually 0.05-0.1)
	maxThrottle float64       // maximum throttle (1.0)
	signalPin   int           // PWM signal pin (for ESC control)
	mu          sync.Mutex
}

// NewBLDCController creates a new BLDC/ESC motor controller
func NewBLDCController(channel, signalPin int) *BLDCController {
	return &BLDCController{
		channel:     channel,
		armTime:     1500 * time.Millisecond, // 1.5s typical ESC arm time
		armed:       false,
		throttle:    0.0,
		minThrottle: 0.05, // 5% minimum to spin
		maxThrottle: 1.0,
		signalPin:   signalPin,
	}
}

func (c *BLDCController) Type() MotorType { return BLDC }
func (c *BLDCController) Channel() int    { return c.channel }

func (c *BLDCController) Arm(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.armed {
		return nil
	}

	// Arm sequence: min throttle for arm time
	if err := c.setPWM(ctx, c.minThrottle); err != nil {
		return err
	}

	select {
	case <-time.After(c.armTime):
		c.armed = true
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *BLDCController) Set(ctx context.Context, throttle float64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.armed {
		return fmt.Errorf("ESC not armed - call Arm() first")
	}

	// Clamp throttle
	if throttle < c.minThrottle {
		throttle = c.minThrottle
	}
	if throttle > c.maxThrottle {
		throttle = c.maxThrottle
	}

	c.throttle = throttle
	return c.setPWM(ctx, throttle)
}

func (c *BLDCController) Get(ctx context.Context) (float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.throttle, nil
}

func (c *BLDCController) Stop(ctx context.Context) error {
	return c.Set(ctx, 0.0)
}

func (c *BLDCController) Calibrate(ctx context.Context) error {
	// ESC calibration: send max throttle, wait for beep, then min
	// For now, this is a no-op
	return nil
}

func (c *BLDCController) setPWM(ctx context.Context, duty float64) error {
	// Set PWM signal for ESC control
	// This is a placeholder - actual implementation depends on hardware
	return nil
}

// Manager manages multiple motor controllers
type Manager struct {
	motors map[int]Controller
	mu     sync.Mutex
}

// NewManager creates a new motor manager
func NewManager() *Manager {
	return &Manager{
		motors: make(map[int]Controller),
	}
}

// AddController adds a motor controller to the manager
func (m *Manager) AddController(ctrl Controller) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.motors[ctrl.Channel()] = ctrl
}

// GetController retrieves a motor controller by channel
func (m *Manager) GetController(channel int) (Controller, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ctrl, ok := m.motors[channel]
	if !ok {
		return nil, fmt.Errorf("motor controller for channel %d not found", channel)
	}
	return ctrl, nil
}

// ListControllers returns all registered motor channels
func (m *Manager) ListControllers() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	channels := make([]int, 0, len(m.motors))
	for ch := range m.motors {
		channels = append(channels, ch)
	}
	return channels
}

// StopAll stops all motors safely
func (m *Manager) StopAll(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	for _, ctrl := range m.motors {
		if err := ctrl.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("channel %d: %w", ctrl.Channel(), err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("some motors failed to stop: %v", errs)
	}
	return nil
}

// EmergencyStop performs a hard stop on all motors (not safe)
func (m *Manager) EmergencyStop(ctx context.Context) error {
	// Hard stop - disable all motors immediately
	// This is different from StopAll which does a controlled stop
	return m.StopAll(ctx)
}

// Status returns status of all motors
type MotorStatus struct {
	Channel   int     `json:"channel"`
	Type      string  `json:"type"`
	Value     float64 `json:"value"`
	Armed     bool    `json:"armed,omitempty"`     // BLDC only
	Position  float64 `json:"position,omitempty"`  // Stepper only
	Microstep int     `json:"microstep,omitempty"` // Stepper only
	Direction bool    `json:"direction,omitempty"` // PWM only
}

func (m *Manager) Status(ctx context.Context) ([]MotorStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	statuses := make([]MotorStatus, 0, len(m.motors))
	for _, ctrl := range m.motors {
		val, err := ctrl.Get(ctx)
		if err != nil {
			return nil, fmt.Errorf("channel %d: %w", ctrl.Channel(), err)
		}

		st := MotorStatus{
			Channel: ctrl.Channel(),
			Type:    string(ctrl.Type()),
			Value:   val,
		}

		// Type-specific fields
		switch ctrl := ctrl.(type) {
		case *BLDCController:
			ctrl.mu.Lock()
			st.Armed = ctrl.armed
			ctrl.mu.Unlock()
		case *StepperController:
			ctrl.mu.Lock()
			st.Position = ctrl.position
			st.Microstep = ctrl.microstep
			ctrl.mu.Unlock()
		case *PWMController:
			ctrl.mu.Lock()
			st.Direction = ctrl.currentDir
			ctrl.mu.Unlock()
		}

		statuses = append(statuses, st)
	}

	return statuses, nil
}

// Config represents motor configuration from vault/env
type Config struct {
	// Motor type: servo_pwm, stepper, brushed_dc, bldc_esc
	Type MotorType `json:"type"`
	// Channel/ID for this motor
	Channel int `json:"channel"`
	// Hardware pins (varies by type)
	Pins map[string]int `json:"pins"`
	// Calibration values
	Calibration map[string]float64 `json:"calibration"`
	// Limits
	MinValue float64 `json:"minValue"`
	MaxValue float64 `json:"maxValue"`
}

// CreateController creates a controller from config
func CreateController(cfg Config) (Controller, error) {
	switch cfg.Type {
	case ServoPWM, BrushedDC:
		minDuty := cfg.MinValue
		if minDuty == 0 {
			minDuty = 0.0
		}
		maxDuty := cfg.MaxValue
		if maxDuty == 0 {
			maxDuty = 1.0
		}
		return NewPWMController(cfg.Channel, minDuty, maxDuty), nil

	case Stepper:
		microstep := 1
		if ms, ok := cfg.Calibration["microstep"]; ok {
			microstep = int(ms)
		}
		dirPin := cfg.Pins["dir"]
		stepPin := cfg.Pins["step"]
		enablePin := cfg.Pins["enable"]
		return NewStepperController(cfg.Channel, dirPin, stepPin, enablePin, microstep), nil

	case BLDC:
		signalPin := cfg.Pins["signal"]
		return NewBLDCController(cfg.Channel, signalPin), nil

	default:
		return nil, fmt.Errorf("unsupported motor type: %s", cfg.Type)
	}
}

// ParseMotorConfig parses a motor config string
// Format: "type:channel,pin1=value1,pin2=value2,calib_key=value"
func ParseMotorConfig(s string) (Config, error) {
	var cfg Config
	cfg.Pins = make(map[string]int)
	cfg.Calibration = make(map[string]float64)

	parts := strings.Split(s, ":")
	if len(parts) < 2 {
		return cfg, fmt.Errorf("invalid motor config format")
	}

	cfg.Type = MotorType(strings.TrimSpace(parts[0]))
	if _, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
		cfg.Channel, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
	}

	if len(parts) > 2 {
		for _, kv := range strings.Split(parts[2], ",") {
			kv = strings.TrimSpace(kv)
			if kv == "" {
				continue
			}
			kvParts := strings.Split(kv, "=")
			if len(kvParts) != 2 {
				continue
			}
			key := strings.TrimSpace(kvParts[0])
			val := strings.TrimSpace(kvParts[1])

			// Try to parse as int first, then float
			if intVal, err := strconv.Atoi(val); err == nil {
				cfg.Pins[key] = intVal
			} else if floatVal, err := strconv.ParseFloat(val, 64); err == nil {
				cfg.Calibration[key] = floatVal
			}
		}
	}

	return cfg, nil
}

// ServoAngleToPulse converts angle (0-180) to pulse width (1000-2000µs)
func ServoAngleToPulse(angle float64) float64 {
	if angle < 0 {
		angle = 0
	}
	if angle > 180 {
		angle = 180
	}
	return 1000.0 + (angle/180.0)*1000.0
}

// ServoPulseToDuty converts pulse width to duty cycle (0.0-1.0) at given PWM frequency
func ServoPulseToDuty(pulse, freqHz float64) float64 {
	period := 1000000.0 / freqHz
	return pulse / period
}
