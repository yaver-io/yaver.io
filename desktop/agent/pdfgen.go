package main

// pdfgen.go — HTML → PDF via the embedded headless Chromium
// (the same one the test SDK uses). Replaces DocRaptor /
// simple invoicing services for the solo dev who just needs
// "give me a PDF of this markup" without standing up LaTeX
// or a separate service.
//
// HTTP surface:
//
//   POST /pdf/render
//     {
//       "html":    "<html>...</html>",
//       "url":     "https://...",             // alternative to html
//       "format":  "A4" | "Letter" | ...,      // default A4
//       "landscape": bool,
//       "marginTop": "1cm", ...
//     }
//   → application/pdf
//
// Only one of html/url is required. HTML goes through a
// data: URL to keep the render purely local (no outbound
// network unless the HTML references remote assets).

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// PDFRenderOptions mirrors the CDP Page.printToPDF shape, minus
// the knobs solo devs never tweak.
type PDFRenderOptions struct {
	HTML         string  `json:"html,omitempty"`
	URL          string  `json:"url,omitempty"`
	Format       string  `json:"format,omitempty"` // A4, Letter, Legal, Tabloid
	Landscape    bool    `json:"landscape,omitempty"`
	PrintBackground bool `json:"printBackground,omitempty"`
	MarginTop    string  `json:"marginTop,omitempty"`
	MarginRight  string  `json:"marginRight,omitempty"`
	MarginBottom string  `json:"marginBottom,omitempty"`
	MarginLeft   string  `json:"marginLeft,omitempty"`
	ScaleFactor  float64 `json:"scale,omitempty"`
	PageRanges   string  `json:"pageRanges,omitempty"`
}

// pageFormat converts the friendly format name to CDP's
// width/height (in inches — CDP's unit for paperWidth).
func pageFormat(name string) (float64, float64) {
	switch name {
	case "Letter":
		return 8.5, 11
	case "Legal":
		return 8.5, 14
	case "Tabloid":
		return 11, 17
	case "A3":
		return 11.7, 16.5
	case "A5":
		return 5.83, 8.27
	case "A4", "":
		fallthrough
	default:
		return 8.27, 11.69
	}
}

// parseCmMargin accepts "1cm" / "10mm" / "0.5in" / "20px" and
// returns inches (CDP's unit). Defaults to 0.4 inch for blank.
func parseCmMargin(s string) float64 {
	if s == "" {
		return 0.4
	}
	var val float64
	var unit string
	fmt.Sscanf(s, "%f%s", &val, &unit)
	switch unit {
	case "cm":
		return val / 2.54
	case "mm":
		return val / 25.4
	case "in":
		return val
	case "px":
		return val / 96
	}
	return val
}

// RenderPDF drives a headless Chromium render and returns the
// PDF bytes. Creates a fresh context per call so concurrent
// renders don't step on each other (Chromium is reused via
// chromedp's browser pool under the hood).
func RenderPDF(opts PDFRenderOptions) ([]byte, error) {
	if opts.HTML == "" && opts.URL == "" {
		return nil, fmt.Errorf("html or url required")
	}
	target := opts.URL
	if target == "" {
		target = "data:text/html;base64," + base64.StdEncoding.EncodeToString([]byte(opts.HTML))
	}

	ctx, cancel := chromedp.NewContext(context.Background())
	defer cancel()
	ctx, tcancel := context.WithTimeout(ctx, 30*time.Second)
	defer tcancel()

	width, height := pageFormat(opts.Format)

	var pdfBytes []byte
	err := chromedp.Run(ctx,
		chromedp.Navigate(target),
		chromedp.ActionFunc(func(ctx context.Context) error {
			p := page.PrintToPDF().
				WithPaperWidth(width).
				WithPaperHeight(height).
				WithLandscape(opts.Landscape).
				WithPrintBackground(opts.PrintBackground).
				WithMarginTop(parseCmMargin(opts.MarginTop)).
				WithMarginBottom(parseCmMargin(opts.MarginBottom)).
				WithMarginLeft(parseCmMargin(opts.MarginLeft)).
				WithMarginRight(parseCmMargin(opts.MarginRight))
			if opts.ScaleFactor > 0 {
				p = p.WithScale(opts.ScaleFactor)
			}
			if opts.PageRanges != "" {
				p = p.WithPageRanges(opts.PageRanges)
			}
			buf, _, err := p.Do(ctx)
			if err != nil {
				return err
			}
			pdfBytes = buf
			return nil
		}),
	)
	return pdfBytes, err
}

// --- HTTP ------------------------------------------------------------------

func (s *HTTPServer) handlePDFRender(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var opts PDFRenderOptions
	if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	pdf, err := RenderPDF(opts)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Length", fmt.Sprint(len(pdf)))
	_, _ = bytes.NewReader(pdf).WriteTo(w)
}
