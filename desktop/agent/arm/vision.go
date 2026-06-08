package arm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/yaver-io/agent/robot"
)

// verifyArmMotion asks the configured vision model whether the ARM moved as
// expected. It reuses robot.AskVision (same provider ladder / creds) but with an
// articulated-arm prompt instead of the Cartesian one, then parses the JSON
// verdict. In "frames" mode the caller's (host) model judges instead.
func verifyArmMotion(ctx context.Context, vc robot.VisionConfig, before, after []byte, expectation string) (Verdict, error) {
	prompt := `You verify a robotic ARM (an articulated manipulator).
You are given a BEFORE image, an AFTER image, and the EXPECTED motion.
Reply with ONLY a compact JSON object, no prose, no markdown:
{"moved":bool,"confidence":0..1,"obstruction":bool,"reason":"short","observed":"short"}
- moved=true ONLY if the arm visibly moved consistent with the expectation.
- obstruction=true if the arm is about to hit something, is jammed, or a person/object is in the path.
- confidence reflects how clearly the images support your answer.
EXPECTED motion: ` + expectation + `
The two images follow (BEFORE then AFTER).`

	// AskVision takes a single image; send the AFTER frame with both embedded via
	// the prompt is lossy, so we embed both as a 2-up by sending AFTER as primary
	// and noting BEFORE is unavailable to single-image models. To keep both, we
	// fall back to robot.VerifyMotion's two-image path when before != after.
	if len(before) > 0 && !sameBytes(before, after) {
		rv, err := robot.VerifyMotion(ctx, vc, before, after, "(articulated arm) "+expectation)
		if err != nil {
			return Verdict{}, err
		}
		return Verdict{Mode: rv.Mode, Moved: rv.Moved, Confidence: rv.Confidence,
			Obstruction: rv.Obstruction, Expectation: expectation, Reason: rv.Reason, Observed: rv.Observed}, nil
	}

	answer, err := robot.AskVision(ctx, vc, after, prompt)
	if err != nil {
		return Verdict{}, err
	}
	return parseArmVerdict(answer, expectation)
}

func parseArmVerdict(s, expectation string) (Verdict, error) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j >= i {
			s = s[i : j+1]
		}
	}
	var v Verdict
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return Verdict{}, err
	}
	v.Mode = "agent"
	v.Expectation = expectation
	return v, nil
}

func jpegDataURL(b []byte) string {
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(b)
}

func sameBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func eqFold(a, b string) bool { return strings.EqualFold(a, b) }
func lowerStr(s string) string { return strings.ToLower(s) }

func clampPct(p int) int {
	if p < 1 {
		return 1
	}
	if p > 100 {
		return 100
	}
	return p
}
