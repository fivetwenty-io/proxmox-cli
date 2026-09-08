package output_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	yaml "github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/output"
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

// TestRenderer_Table_HyphenatedHeaderRendersVerbatim guards against
// tablewriter's header AutoFormat, which (when left at its default) splits a
// hyphen flanked by uppercase letters into its own token and rejoins it with
// spaces, turning "ACCESS-KEY" into "ACCESS - KEY". Every header this
// renderer prints is already a finished, uppercase label, so it must come out
// unchanged.
func TestRenderer_Table_HyphenatedHeaderRendersVerbatim(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	res := output.Result{
		Headers: []string{"ID", "ACCESS-KEY"},
		Rows:    [][]string{{"minio-lab", "AKIAMINIO"}},
	}
	require.NoError(t, r.Render(&buf, res, output.FormatTable))
	out := buf.String()
	require.Contains(t, out, "ACCESS-KEY")
	require.NotContains(t, out, "ACCESS - KEY")
}

// TestRenderer_Table_PlainHeaderUnchanged confirms disabling AutoFormat
// doesn't alter a header that was already plain.
func TestRenderer_Table_PlainHeaderUnchanged(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	require.NoError(t, r.Render(&buf, fixture(), output.FormatTable))
	out := buf.String()
	require.Contains(t, out, "NAME")
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
	require.Empty(t, buf.String(),
		"a Result with nothing in it prints nothing: there are no headers to draw a frame around")
}

// TestRenderer_Table_EmptyRowsKeepsHeaders separates the two empties a command
// can produce. A list that returned no entries still has columns, and printing
// nothing there would leave the operator unable to tell "no results" from "the
// command produced no output".
func TestRenderer_Table_EmptyRowsKeepsHeaders(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	require.NoError(t, r.Render(&buf,
		output.Result{Headers: []string{"NAME", "NODE"}, Rows: [][]string{}}, output.FormatTable))

	out := buf.String()
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "NODE")
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
	require.Empty(t, buf.String(),
		"plain output is for piping: an empty Result must add no line for a reader to strip")
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
	require.Equal(t, "{}", strings.TrimSpace(buf.String()),
		"an empty Result is an empty object, never null: `| jq` must not have to guard against it")
}

// TestRenderer_JSON_EmptyRawIsAnArray pins the list case separately. A command
// whose list came back empty must emit [], since a consumer doing `| jq length`
// gets 0 from an array and an error from null.
func TestRenderer_JSON_EmptyRawIsAnArray(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	require.NoError(t, r.Render(&buf,
		output.Result{Headers: []string{"NAME"}, Rows: [][]string{}, Raw: []map[string]any{}},
		output.FormatJSON))
	require.Equal(t, "[]", strings.TrimSpace(buf.String()))
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
	require.Equal(t, "{}", strings.TrimSpace(buf.String()),
		"YAML mirrors JSON's empty shape, so a caller can switch formats without switching parsers")
}

// TestRenderer_YAML_EmptyRawIsAnArray is the YAML half of the empty-list
// contract, kept alongside the JSON one so the two cannot drift apart.
func TestRenderer_YAML_EmptyRawIsAnArray(t *testing.T) {
	t.Parallel()
	r := output.New()
	var buf bytes.Buffer
	require.NoError(t, r.Render(&buf,
		output.Result{Headers: []string{"NAME"}, Rows: [][]string{}, Raw: []map[string]any{}},
		output.FormatYAML))
	require.Equal(t, "[]", strings.TrimSpace(buf.String()))
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

// ---- YAML: raw JSON payloads ------------------------------------------------
//
// Many commands hand the renderer the SDK's untouched json.RawMessage (or a
// pointer to one, a slice of them, or a struct carrying one). goccy/go-yaml
// sees []byte and emits a sequence of integers, so `-o yaml` must re-encode
// the value as the document `-o json` would print.

func TestRenderer_YAML_RawMessageIsDecoded(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"/sdn/zones/provo/vlan54":{"VM.Audit":1,"SDN.Use":1},"/":{"Sys.Audit":1}}`)

	for name, v := range map[string]any{
		"value":   raw,
		"pointer": &raw,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			require.NoError(t, output.New().Render(&buf, output.Result{Raw: v}, output.FormatYAML))
			out := buf.String()
			require.NotContains(t, out, "- 123", "must not dump the bytes: %s", out)

			var parsed map[string]map[string]int
			require.NoError(t, yaml.Unmarshal(buf.Bytes(), &parsed), "got: %s", out)
			require.Equal(t, 1, parsed["/sdn/zones/provo/vlan54"]["VM.Audit"])
			require.Equal(t, 1, parsed["/"]["Sys.Audit"])
		})
	}
}

func TestRenderer_YAML_RawMessageSliceIsDecoded(t *testing.T) {
	t.Parallel()
	raws := []json.RawMessage{
		json.RawMessage(`{"vmid":100,"name":"alpha"}`),
		json.RawMessage(`{"vmid":101,"name":"beta"}`),
	}
	var buf bytes.Buffer
	require.NoError(t, output.New().Render(&buf, output.Result{Raw: raws}, output.FormatYAML))

	var parsed []map[string]any
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &parsed), "got: %s", buf.String())
	require.Len(t, parsed, 2)
	require.Equal(t, "beta", parsed[1]["name"])
	require.EqualValues(t, 101, parsed[1]["vmid"])
}

func TestRenderer_YAML_RawMessageFieldInStructIsDecoded(t *testing.T) {
	t.Parallel()
	type entry struct {
		Node string          `json:"node"`
		UID  json.RawMessage `json:"uid"`
	}
	var buf bytes.Buffer
	require.NoError(t, output.New().Render(&buf,
		output.Result{Raw: []entry{{Node: "pve-0", UID: json.RawMessage(`"root@pam"`)}}},
		output.FormatYAML))
	out := buf.String()
	require.Contains(t, out, "uid: root@pam", "got: %s", out)
}

// TestRenderer_YAML_ScalarsMatchJSON pins the scalar rendering: integers stay
// integers (not 1.0 and not "1"), floats stay floats, and strings that look
// like numbers stay quoted strings.
func TestRenderer_YAML_ScalarsMatchJSON(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"int":1,"big":12345678901234,"float":1.5,"str":"1","neg":-3,"nil":null,"ok":true}`)
	var buf bytes.Buffer
	require.NoError(t, output.New().Render(&buf, output.Result{Raw: raw}, output.FormatYAML))
	out := buf.String()
	require.Contains(t, out, "int: 1\n", "got: %s", out)
	require.Contains(t, out, "big: 12345678901234\n", "got: %s", out)
	require.Contains(t, out, "float: 1.5\n", "got: %s", out)
	require.Contains(t, out, `str: "1"`, "got: %s", out)
	require.Contains(t, out, "neg: -3\n", "got: %s", out)
	require.Contains(t, out, "nil: null\n", "got: %s", out)
	require.Contains(t, out, "ok: true\n", "got: %s", out)
}

// TestRenderer_YAML_RawKeyOrderMatchesJSON: the YAML document lists keys in the
// same order the JSON document does (server order for raw payloads, field
// order for structs), so the two formats stay a syntax swap apart.
func TestRenderer_YAML_RawKeyOrderMatchesJSON(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"zeta":1,"alpha":{"two":2,"one":3},"mid":[]}`)
	var buf bytes.Buffer
	require.NoError(t, output.New().Render(&buf, output.Result{Raw: raw}, output.FormatYAML))
	require.Equal(t, "zeta: 1\nalpha:\n  two: 2\n  one: 3\nmid: []\n", buf.String())
}

func TestRenderer_YAML_InvalidRawMessageErrors(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	err := output.New().Render(&buf, output.Result{Raw: json.RawMessage(`{not json`)}, output.FormatYAML)
	require.Error(t, err)
}
