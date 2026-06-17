package main

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func solidJPEGBytes(t *testing.T, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

func TestFrameBrightnessAndDiff(t *testing.T) {
	black := solidJPEGBytes(t, color.RGBA{0, 0, 0, 255})
	white := solidJPEGBytes(t, color.RGBA{255, 255, 255, 255})

	if !frameIsBlack(black) {
		t.Error("solid black frame should be detected as black")
	}
	if frameIsBlack(white) {
		t.Error("solid white frame should NOT be black")
	}

	same, err := frameDiffScore(black, black)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if same > 0.02 {
		t.Errorf("identical frames should have ~0 diff, got %f", same)
	}

	diff, err := frameDiffScore(black, white)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if diff < 0.5 {
		t.Errorf("black vs white should be a large diff, got %f", diff)
	}
}

func TestRunActivityStepsVerifyRetry(t *testing.T) {
	// One step that runs fine but whose verify fails the first attempt and
	// passes on the second. Retry:1 should make the activity complete.
	steps := []homeStep{{Device: "tv", Key: "power_on", Verify: "camera:cam1", Retry: 1}}

	runCalls, verifyCalls := 0, 0
	res, completed := runActivitySteps(steps,
		func(st homeStep) error { runCalls++; return nil },
		func(st homeStep) error {
			verifyCalls++
			if verifyCalls < 2 {
				return errors.New("frame dark")
			}
			return nil
		},
	)
	if !completed {
		t.Error("expected completion after a successful retry")
	}
	if runCalls != 2 || verifyCalls != 2 {
		t.Errorf("expected 2 run + 2 verify attempts, got run=%d verify=%d", runCalls, verifyCalls)
	}
	if len(res) != 1 || !res[0].OK {
		t.Errorf("step should be recorded OK after retry: %+v", res)
	}

	// Verify that never passes → with default abort and Retry:1, the step
	// exhausts attempts and the activity stops.
	verifyCalls = 0
	res, completed = runActivitySteps(steps,
		func(st homeStep) error { return nil },
		func(st homeStep) error { verifyCalls++; return errors.New("still dark") },
	)
	if completed {
		t.Error("expected abort when verify never passes")
	}
	if verifyCalls != 2 { // 1 + Retry:1
		t.Errorf("expected 2 verify attempts (1 + retry), got %d", verifyCalls)
	}
	if res[0].OK {
		t.Error("step should be recorded failed")
	}
}
