package testkit

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"
)

// Suite is the aggregate of every spec result in one run, used for
// reporting and exit code.
type Suite struct {
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Results    []*Result `json:"-"`
	// JSONResults is a serializable view of Results — chromedp errors
	// don't marshal cleanly so we flatten things in MarshalJSON.
}

// Passed returns true when every spec in the suite passed.
func (s *Suite) Passed() bool {
	for _, r := range s.Results {
		if r == nil || !r.Passed {
			return false
		}
	}
	return true
}

// Counts returns (total, passed, failed).
func (s *Suite) Counts() (total, passed, failed int) {
	for _, r := range s.Results {
		total++
		if r != nil && r.Passed {
			passed++
		} else {
			failed++
		}
	}
	return
}

// MarshalJSON flattens errors into strings so JSON consumers (mobile app,
// CI tools) can parse the output without knowing Go error semantics.
func (s *Suite) MarshalJSON() ([]byte, error) {
	type stepView struct {
		Index          int    `json:"index"`
		Phase          string `json:"phase"`
		Description    string `json:"description"`
		DurationMS     int64  `json:"duration_ms"`
		Error          string `json:"error,omitempty"`
		ScreenshotPath string `json:"screenshot,omitempty"`
	}
	type resultView struct {
		Name       string     `json:"name"`
		Path       string     `json:"path"`
		Target     Target     `json:"target"`
		Passed     bool       `json:"passed"`
		StartedAt  time.Time  `json:"started_at"`
		FinishedAt time.Time  `json:"finished_at"`
		DurationMS int64      `json:"duration_ms"`
		Error      string     `json:"error,omitempty"`
		Steps      []stepView `json:"steps"`
	}
	type suiteView struct {
		StartedAt  time.Time    `json:"started_at"`
		FinishedAt time.Time    `json:"finished_at"`
		DurationMS int64        `json:"duration_ms"`
		Total      int          `json:"total"`
		Passed     int          `json:"passed"`
		Failed     int          `json:"failed"`
		Results    []resultView `json:"results"`
	}
	total, passed, failed := s.Counts()
	view := suiteView{
		StartedAt:  s.StartedAt,
		FinishedAt: s.FinishedAt,
		DurationMS: s.FinishedAt.Sub(s.StartedAt).Milliseconds(),
		Total:      total,
		Passed:     passed,
		Failed:     failed,
		Results:    make([]resultView, 0, len(s.Results)),
	}
	for _, r := range s.Results {
		if r == nil {
			continue
		}
		rv := resultView{
			Name:       r.Spec.Name,
			Path:       r.Spec.Path,
			Target:     r.Spec.Target,
			Passed:     r.Passed,
			StartedAt:  r.StartedAt,
			FinishedAt: r.FinishedAt,
			DurationMS: r.Duration().Milliseconds(),
		}
		if r.Err != nil {
			rv.Error = r.Err.Error()
		}
		for _, st := range r.Steps {
			sv := stepView{
				Index:          st.Index,
				Phase:          st.Phase,
				Description:    st.Description,
				DurationMS:     st.Duration.Milliseconds(),
				ScreenshotPath: st.ScreenshotPath,
			}
			if st.Err != nil {
				sv.Error = st.Err.Error()
			}
			rv.Steps = append(rv.Steps, sv)
		}
		view.Results = append(view.Results, rv)
	}
	return json.Marshal(view)
}

// WriteJSON dumps the suite as pretty JSON to w.
func (s *Suite) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

// WriteJUnit emits a JUnit XML report so the same run can be uploaded to
// GitHub Actions / Jenkins / wherever expects JUnit. Mirrors the shape
// jest-junit and pytest emit.
func (s *Suite) WriteJUnit(w io.Writer) error {
	type failure struct {
		XMLName xml.Name `xml:"failure"`
		Message string   `xml:"message,attr"`
		Body    string   `xml:",chardata"`
	}
	type testcase struct {
		XMLName   xml.Name `xml:"testcase"`
		Classname string   `xml:"classname,attr"`
		Name      string   `xml:"name,attr"`
		Time      string   `xml:"time,attr"`
		Failure   *failure `xml:",omitempty"`
	}
	type testsuite struct {
		XMLName  xml.Name   `xml:"testsuite"`
		Name     string     `xml:"name,attr"`
		Tests    int        `xml:"tests,attr"`
		Failures int        `xml:"failures,attr"`
		Time     string     `xml:"time,attr"`
		Cases    []testcase `xml:"testcase"`
	}
	type testsuites struct {
		XMLName  xml.Name    `xml:"testsuites"`
		Tests    int         `xml:"tests,attr"`
		Failures int         `xml:"failures,attr"`
		Time     string      `xml:"time,attr"`
		Suites   []testsuite `xml:"testsuite"`
	}

	total, _, failed := s.Counts()
	root := testsuites{
		Tests:    total,
		Failures: failed,
		Time:     fmt.Sprintf("%.3f", s.FinishedAt.Sub(s.StartedAt).Seconds()),
	}
	suite := testsuite{
		Name:     "yaver-test-sdk",
		Tests:    total,
		Failures: failed,
		Time:     fmt.Sprintf("%.3f", s.FinishedAt.Sub(s.StartedAt).Seconds()),
	}
	for _, r := range s.Results {
		if r == nil {
			continue
		}
		tc := testcase{
			Classname: string(r.Spec.Target),
			Name:      r.Spec.Name,
			Time:      fmt.Sprintf("%.3f", r.Duration().Seconds()),
		}
		if !r.Passed {
			msg := "spec failed"
			body := r.Err
			if body == nil {
				for _, st := range r.Steps {
					if st.Err != nil {
						msg = fmt.Sprintf("step %d (%s): %s", st.Index, st.Description, st.Err.Error())
						body = st.Err
						break
					}
				}
			} else {
				msg = body.Error()
			}
			f := &failure{Message: msg}
			if body != nil {
				f.Body = body.Error()
			}
			tc.Failure = f
		}
		suite.Cases = append(suite.Cases, tc)
	}
	root.Suites = []testsuite{suite}
	if _, err := w.Write([]byte(xml.Header)); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(root); err != nil {
		return err
	}
	_, err := w.Write([]byte("\n"))
	return err
}

// WriteTTY prints a colored, human-friendly summary suitable for a
// terminal. Plays well with `yaver test run` invocations.
func (s *Suite) WriteTTY(w io.Writer) {
	total, passed, failed := s.Counts()
	for _, r := range s.Results {
		if r == nil {
			continue
		}
		mark := "✓"
		if !r.Passed {
			mark = "✗"
		}
		fmt.Fprintf(w, "%s %s  (%s)\n", mark, r.Spec.Name, r.Duration().Round(time.Millisecond))
		if r.Err != nil {
			fmt.Fprintf(w, "    error: %s\n", r.Err.Error())
		}
		for _, st := range r.Steps {
			if st.Err != nil {
				fmt.Fprintf(w, "    [%s %d] %s — FAIL: %s\n", st.Phase, st.Index, st.Description, st.Err.Error())
				if st.ScreenshotPath != "" {
					fmt.Fprintf(w, "        screenshot: %s\n", st.ScreenshotPath)
				}
			}
		}
	}
	fmt.Fprintln(w, strings.Repeat("─", 60))
	fmt.Fprintf(w, "%d total, %d passed, %d failed (%s)\n",
		total, passed, failed, s.FinishedAt.Sub(s.StartedAt).Round(time.Millisecond))
}
