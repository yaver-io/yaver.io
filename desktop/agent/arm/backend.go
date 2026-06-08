package arm

import "context"

// Backend is the per-robot driver. Every method is DOF-agnostic — joints are
// passed as a name→value map, never as fixed X/Y/Z. A backend that cannot do
// Cartesian moves returns ErrNoCartesian from Pose/MoveLinear and reports
// HasCartesian=false; everything else still works in joint space.
//
// Describe is the heart of the "read parameters, don't hardcode" contract: it
// returns the arm's DOF + joint limits, ideally read FROM THE ROBOT, falling
// back to whatever the config/UI defined.
//
// velPct/accPct are 0..100 of the robot's configured max — the standard cobot
// speed/accel override (Fairino "vel"/"acc", UR "speed"/"acceleration").
type Backend interface {
	Name() string
	Connect(ctx context.Context) error
	Close() error

	// Describe reports the arm's parametric definition (DOF, joints, units).
	Describe(ctx context.Context) (ArmInfo, error)

	Status(ctx context.Context) (ArmStatus, error)
	// Enable powers/clears the arm so motion is accepted (cobot "robot enable").
	Enable(ctx context.Context, on bool) error

	// JointState returns fresh joint positions (ordered as ArmInfo.Joints).
	JointState(ctx context.Context) ([]JointState, error)
	// Pose returns the current Cartesian TCP pose; ErrNoCartesian if unsupported.
	Pose(ctx context.Context) (Pose, error)

	// MoveJoints (MoveJ) commands absolute joint targets (name→value).
	MoveJoints(ctx context.Context, targets map[string]float64, velPct, accPct int) error
	// MoveLinear (MoveL) commands an absolute Cartesian pose; ErrNoCartesian if
	// the arm has no Cartesian support.
	MoveLinear(ctx context.Context, p Pose, velPct, accPct int) error

	// WaitIdle blocks until motion completes (the arm equivalent of M400).
	WaitIdle(ctx context.Context) error
	// Stop halts motion; EStop latches a safety stop.
	Stop(ctx context.Context) error
	EStop(ctx context.Context) error

	// FreeDrive toggles hand-guiding / leadthrough (the "learning mode": servos
	// go compliant so you physically move the arm and capture waypoints). Returns
	// ErrNoFreeDrive on backends that can't do it. (Fairino DragTeachSwitch,
	// myCobot release-servos, PAROL6 via the bridge.)
	FreeDrive(ctx context.Context, on bool) error

	// Raw sends a backend-specific command (an XML-RPC method + args, a TCP line)
	// for power users / wiring new robots by parameter.
	Raw(ctx context.Context, cmd string) (string, error)
}

// ErrNoCartesian is returned by Pose/MoveLinear on joint-only backends.
var ErrNoCartesian = errConst("this arm backend has no Cartesian (TCP-pose) support; use joint moves")

// ErrNoFreeDrive is returned by FreeDrive on backends without hand-guiding.
var ErrNoFreeDrive = errConst("this arm has no free-drive / hand-guide mode")

type errConst string

func (e errConst) Error() string { return string(e) }
