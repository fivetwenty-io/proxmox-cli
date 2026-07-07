package output_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	yaml "github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/output"
)

// fixture builds a standard 3-row Result used across multiple tests.
func fixture() output.Result {
	return output.Result{
		Headers: []string{"ID", "NAME", "STATUS"},
		Rows: [][]string{
			{"1", "alpha", "running"},
			{"2", "beta", "stopped"},
			{"3", "gamma", "running"},
		},
		Raw: []map[string]string{
			{"id": "1", "name": "alpha", "status": "running"},
			{"id": "2", "name": "beta", "status": "stopped"},
			{"id": "3", "name": "gamma", "status": "running"},
		},
	}
}

// singleFixture builds a Result with only Single populated.
func singleFixture() output.Result {
	return output.Result{
		Single: map[string]string{
			"id":     "42",
			"name":   "myvm",
			"status": "running",
		},
		Raw: map[string]string{
			"id":     "42",
			"name":   "myvm",
			"status": "running",
		},
	}
}

// ---- New -------------------------------------------------------------------

func TestNew_ReturnsRenderer(t *testing.T) {
	t.Parallel()
	r := output.New()
	require.NotNil(t, r)
}

// ---- Format constants ------------------------------------------------------

func TestFormatConstants(t *testing.T) {
	t.Parallel()
	require.Equal(t, output.Format("table"), output.FormatTable)
	require.Equal(t, output.Format("ascii"), output.FormatASCII)
	require.Equal(t, output.Format("plain"), output.FormatPlain)
	require.Equal(t, output.Format("json"), output.FormatJSON)
	require.Equal(t, output.Format("yaml"), output.FormatYAML)
}

// ---- Unknown format --------------------------------------------------------

func TestRender_UnknownFormat_ReturnsError(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	err := r.Render(&buf, fixture(), output.Format("csv"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown output format")
	require.Contains(t, err.Error(), "csv")
}

// ---- Table renderer --------------------------------------------------------

func TestRenderer_Table_Headers(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	require.NoError(t, r.Render(&buf, fixture(), output.FormatTable))
	out := buf.String()
	require.Contains(t, out, "ID")
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "STATUS")
}

func TestRenderer_Table_Rows(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	require.NoError(t, r.Render(&buf, fixture(), output.FormatTable))
	out := buf.String()
	require.Contains(t, out, "alpha")
	require.Contains(t, out, "beta")
	require.Contains(t, out, "gamma")
}

func TestRenderer_Table_Single(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	res := singleFixture()
	res.Raw = nil // force table path to use Single
	require.NoError(t, r.Render(&buf, res, output.FormatTable))
	out := buf.String()
	require.Contains(t, out, "KEY")
	require.Contains(t, out, "VALUE")
	require.Contains(t, out, "name")
	require.Contains(t, out, "myvm")
}

func TestRenderer_Table_Message(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	res := output.Result{Message: "Operation completed successfully."}
	require.NoError(t, r.Render(&buf, res, output.FormatTable))
	require.Contains(t, buf.String(), "Operation completed successfully.")
}

func TestRenderer_ASCII(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	require.NoError(t, r.Render(&buf, fixture(), output.FormatASCII))
	out := buf.String()
	// ASCII borders use '+' and '-' not Unicode box-drawing characters.
	require.Contains(t, out, "+")
	require.Contains(t, out, "-")
	require.NotContains(t, out, "─")
}

func TestRenderer_Table_EmptyResult_NoError(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	require.NoError(t, r.Render(&buf, output.Result{}, output.FormatTable))
}

// ---- Plain renderer --------------------------------------------------------

func TestRenderer_Plain_Headers(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	require.NoError(t, r.Render(&buf, fixture(), output.FormatPlain))
	out := buf.String()
	// Headers should appear on the first line.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.NotEmpty(t, lines)
	firstLine := lines[0]
	require.Contains(t, firstLine, "ID")
	require.Contains(t, firstLine, "NAME")
	require.Contains(t, firstLine, "STATUS")
}

func TestRenderer_Plain_Rows(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	require.NoError(t, r.Render(&buf, fixture(), output.FormatPlain))
	out := buf.String()
	require.Contains(t, out, "alpha")
	require.Contains(t, out, "stopped")
}

func TestRenderer_Plain_Single(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	res := singleFixture()
	res.Raw = nil
	require.NoError(t, r.Render(&buf, res, output.FormatPlain))
	out := buf.String()
	require.Contains(t, out, "name")
	require.Contains(t, out, "myvm")
}

func TestRenderer_Plain_Message(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	res := output.Result{Message: "Deleted."}
	require.NoError(t, r.Render(&buf, res, output.FormatPlain))
	require.Contains(t, buf.String(), "Deleted.")
}

func TestRenderer_Plain_EmptyResult_NoError(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	require.NoError(t, r.Render(&buf, output.Result{}, output.FormatPlain))
}

// ---- JSON renderer ---------------------------------------------------------

func TestRenderer_JSON_RawArray(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	require.NoError(t, r.Render(&buf, fixture(), output.FormatJSON))

	var parsed []map[string]string
	require.NoError(t, json.Unmarshal(buf.Bytes(), &parsed),
		"output must be valid JSON; got: %s", buf.String())
	require.Len(t, parsed, 3)
	require.Equal(t, "alpha", parsed[0]["name"])
}

func TestRenderer_JSON_SyntheticTable(t *testing.T) {
	t.Parallel()
	// No Raw — should emit {headers, rows}.
	r := output.New()
	var buf bytes.Buffer
	res := fixture()
	res.Raw = nil
	require.NoError(t, r.Render(&buf, res, output.FormatJSON))

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &parsed),
		"output must be valid JSON; got: %s", buf.String())
	require.Contains(t, parsed, "headers")
	require.Contains(t, parsed, "rows")
}

func TestRenderer_JSON_Single(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	res := singleFixture()
	res.Raw = nil
	require.NoError(t, r.Render(&buf, res, output.FormatJSON))

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &parsed),
		"output must be valid JSON; got: %s", buf.String())
	require.Contains(t, parsed, "data")
}

func TestRenderer_JSON_Message(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	res := output.Result{Message: "hello"}
	require.NoError(t, r.Render(&buf, res, output.FormatJSON))

	var parsed map[string]string
	require.NoError(t, json.Unmarshal(buf.Bytes(), &parsed))
	require.Equal(t, "hello", parsed["message"])
}

func TestRenderer_JSON_Empty(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	require.NoError(t, r.Render(&buf, output.Result{}, output.FormatJSON))
	// Should produce valid JSON (empty object or similar).
	var parsed any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &parsed))
}

// ---- YAML renderer ---------------------------------------------------------

func TestRenderer_YAML_RawArray(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	require.NoError(t, r.Render(&buf, fixture(), output.FormatYAML))

	var parsed []map[string]string
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &parsed),
		"output must be valid YAML; got: %s", buf.String())
	require.Len(t, parsed, 3)
	require.Equal(t, "alpha", parsed[0]["name"])
}

func TestRenderer_YAML_SyntheticTable(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	res := fixture()
	res.Raw = nil
	require.NoError(t, r.Render(&buf, res, output.FormatYAML))

	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &parsed),
		"output must be valid YAML; got: %s", buf.String())
	require.Contains(t, parsed, "headers")
	require.Contains(t, parsed, "rows")
}

func TestRenderer_YAML_Single(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	res := singleFixture()
	res.Raw = nil
	require.NoError(t, r.Render(&buf, res, output.FormatYAML))

	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &parsed),
		"output must be valid YAML; got: %s", buf.String())
	require.Contains(t, parsed, "data")
}

func TestRenderer_YAML_Message(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	res := output.Result{Message: "done"}
	require.NoError(t, r.Render(&buf, res, output.FormatYAML))

	var parsed map[string]string
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &parsed))
	require.Equal(t, "done", parsed["message"])
}

func TestRenderer_YAML_Empty(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	require.NoError(t, r.Render(&buf, output.Result{}, output.FormatYAML))
	// Should produce valid (possibly empty) YAML.
	var parsed any
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &parsed))
}

// TestRenderer_SingleMap_JSONAndYAMLSameShape verifies that a single-map Result
// (Raw nil) renders to structurally identical documents under JSON and YAML:
// both must place the data under a "data" object keyed by the original field
// names, not a key/value pair list.
func TestRenderer_SingleMap_JSONAndYAMLSameShape(t *testing.T) {
	t.Parallel()
	r := output.New()
	res := singleFixture()
	res.Raw = nil

	var jbuf, ybuf bytes.Buffer
	require.NoError(t, r.Render(&jbuf, res, output.FormatJSON))
	require.NoError(t, r.Render(&ybuf, res, output.FormatYAML))

	var jparsed, yparsed map[string]any
	require.NoError(t, json.Unmarshal(jbuf.Bytes(), &jparsed))
	require.NoError(t, yaml.Unmarshal(ybuf.Bytes(), &yparsed))

	jdata, ok := jparsed["data"].(map[string]any)
	require.True(t, ok, "JSON data must be an object; got: %s", jbuf.String())
	ydata, ok := yparsed["data"].(map[string]any)
	require.True(t, ok, "YAML data must be an object, not a key/value list; got: %s", ybuf.String())

	require.Equal(t, jdata, ydata)
	require.Equal(t, "42", ydata["id"])
	require.Equal(t, "myvm", ydata["name"])
}

// ---- All formats on same fixture -------------------------------------------

func TestRenderer_AllFormats_NoError(t *testing.T) {
	t.Parallel()
	r := output.New()
	fmts := []output.Format{
		output.FormatTable,
		output.FormatPlain,
		output.FormatJSON,
		output.FormatYAML,
	}
	for _, f := range fmts {
		t.Run(string(f), func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, r.Render(&buf, fixture(), f))
			require.NotEmpty(t, buf.String())
		})
	}
}
