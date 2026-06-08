package arm

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// xmlrpcClient is a tiny, dependency-free XML-RPC client — the transport
// Fairino's official SDK (Robot.RPC) speaks to the controller. Supports the
// value types Fairino uses: int, double, string, boolean, and arrays thereof.
type xmlrpcClient struct {
	url    string
	client *http.Client
}

func newXMLRPCClient(url string, timeout time.Duration) *xmlrpcClient {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &xmlrpcClient{url: url, client: &http.Client{Timeout: timeout}}
}

// call invokes method with params and returns the (single) response value.
func (c *xmlrpcClient) call(ctx context.Context, method string, params ...any) (xmlrpcValue, error) {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0"?><methodCall><methodName>`)
	_ = xml.EscapeText(&b, []byte(method))
	b.WriteString(`</methodName><params>`)
	for _, p := range params {
		b.WriteString("<param>")
		encodeXMLRPCValue(&b, p)
		b.WriteString("</param>")
	}
	b.WriteString("</params></methodCall>")

	req, err := http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewReader(b.Bytes()))
	if err != nil {
		return xmlrpcValue{}, err
	}
	req.Header.Set("Content-Type", "text/xml")
	resp, err := c.client.Do(req)
	if err != nil {
		return xmlrpcValue{}, fmt.Errorf("xmlrpc %s: %w", method, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return xmlrpcValue{}, fmt.Errorf("xmlrpc %s: http %d: %s", method, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return parseXMLRPCResponse(raw)
}

func encodeXMLRPCValue(b *bytes.Buffer, v any) {
	b.WriteString("<value>")
	switch t := v.(type) {
	case int:
		b.WriteString("<int>" + strconv.Itoa(t) + "</int>")
	case int64:
		b.WriteString("<int>" + strconv.FormatInt(t, 10) + "</int>")
	case float64:
		b.WriteString("<double>" + strconv.FormatFloat(t, 'f', -1, 64) + "</double>")
	case bool:
		if t {
			b.WriteString("<boolean>1</boolean>")
		} else {
			b.WriteString("<boolean>0</boolean>")
		}
	case string:
		b.WriteString("<string>")
		_ = xml.EscapeText(b, []byte(t))
		b.WriteString("</string>")
	case []float64:
		b.WriteString("<array><data>")
		for _, e := range t {
			encodeXMLRPCValue(b, e)
		}
		b.WriteString("</data></array>")
	case []int:
		b.WriteString("<array><data>")
		for _, e := range t {
			encodeXMLRPCValue(b, e)
		}
		b.WriteString("</data></array>")
	case []any:
		b.WriteString("<array><data>")
		for _, e := range t {
			encodeXMLRPCValue(b, e)
		}
		b.WriteString("</data></array>")
	default:
		b.WriteString("<string></string>")
	}
	b.WriteString("</value>")
}

// xmlrpcValue is a parsed response value: a scalar (Str) or an array (Arr).
type xmlrpcValue struct {
	Str   string
	Arr   []xmlrpcValue
	Fault bool
}

func (v xmlrpcValue) Float() (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(v.Str), 64)
	return f, err == nil
}

// Floats flattens a (possibly nested) array of numeric values to []float64.
func (v xmlrpcValue) Floats() []float64 {
	var out []float64
	if len(v.Arr) == 0 {
		if f, ok := v.Float(); ok {
			out = append(out, f)
		}
		return out
	}
	for _, e := range v.Arr {
		out = append(out, e.Floats()...)
	}
	return out
}

// parseXMLRPCResponse extracts the first param value (or a fault) from a
// methodResponse body. It walks the XML generically so it tolerates the exact
// nesting differences between controllers.
func parseXMLRPCResponse(raw []byte) (xmlrpcValue, error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	inFault := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return xmlrpcValue{}, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "fault":
			inFault = true
		case "value":
			v, err := decodeXMLRPCValue(dec, se)
			if err != nil {
				return xmlrpcValue{}, err
			}
			if inFault {
				v.Fault = true
				return v, fmt.Errorf("xmlrpc fault: %s", faultString(v))
			}
			return v, nil
		}
	}
	return xmlrpcValue{}, fmt.Errorf("xmlrpc: no value in response")
}

// decodeXMLRPCValue decodes a single <value>…</value> (already entered at se).
func decodeXMLRPCValue(dec *xml.Decoder, start xml.StartElement) (xmlrpcValue, error) {
	var v xmlrpcValue
	for {
		tok, err := dec.Token()
		if err != nil {
			return v, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "array":
				arr, err := decodeXMLRPCArray(dec)
				if err != nil {
					return v, err
				}
				v.Arr = arr
			case "struct":
				_ = dec.Skip() // structs are flattened to empty (Fairino getters don't use them)
			default:
				// scalar type tag (int/i4/double/string/boolean/...)
				var s string
				if err := dec.DecodeElement(&s, &t); err != nil {
					return v, err
				}
				v.Str = strings.TrimSpace(s)
			}
		case xml.CharData:
			if strings.TrimSpace(string(t)) != "" && v.Str == "" {
				v.Str = strings.TrimSpace(string(t)) // untyped <value>text</value>
			}
		case xml.EndElement:
			if t.Name.Local == "value" {
				return v, nil
			}
		}
	}
}

func decodeXMLRPCArray(dec *xml.Decoder) ([]xmlrpcValue, error) {
	var out []xmlrpcValue
	for {
		tok, err := dec.Token()
		if err != nil {
			return out, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "value" {
				v, err := decodeXMLRPCValue(dec, t)
				if err != nil {
					return out, err
				}
				out = append(out, v)
			}
		case xml.EndElement:
			if t.Name.Local == "array" {
				return out, nil
			}
		}
	}
}

func faultString(v xmlrpcValue) string {
	if v.Str != "" {
		return v.Str
	}
	for _, e := range v.Arr {
		if e.Str != "" {
			return e.Str
		}
	}
	return "unknown"
}
