package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// yamlDocSources are the operator-facing documents whose fenced yaml blocks
// must load as a pmx configuration file. The paths are relative to this
// package's directory, which is where go test runs.
var yamlDocSources = []string{
	filepath.Join("..", "..", "README.md"),
	filepath.Join("pages", "pmx-config.5.md"),
}

// yamlBlock is one fenced yaml block, with the 1-based line its opening
// fence sits on so a failure points at the source.
type yamlBlock struct {
	line int
	body string
}

// fenceRE matches a fence line, capturing its backtick or tilde run and the
// info string after it. A fence may be indented, as it is inside a list item.
var fenceRE = regexp.MustCompile("^[ \t]*(`{3,}|~{3,})[ \t]*([^ \t`]*)")

// extractYAMLBlocks returns every fenced block whose info string is yaml or
// yml, in document order. A block is closed only by a fence of the same
// character that is at least as long as the one that opened it, as
// CommonMark requires, so a shorter fence inside a block stays content. An
// unclosed block is an error rather than a silently skipped example.
func extractYAMLBlocks(doc string) ([]yamlBlock, error) {
	var (
		blocks  []yamlBlock
		current *yamlBlock
		opener  string
		indent  string
		body    strings.Builder
	)

	sc := bufio.NewScanner(strings.NewReader(doc))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	inOther := false

	for sc.Scan() {
		lineNo++
		line := sc.Text()
		m := fenceRE.FindStringSubmatch(line)

		switch {
		case current != nil:
			if m != nil && m[1][0] == opener[0] && len(m[1]) >= len(opener) && m[2] == "" {
				current.body = body.String()
				blocks = append(blocks, *current)
				current = nil
				body.Reset()

				continue
			}

			// Strip the fence's own indentation, so a block nested in a list
			// item parses as the operator would paste it.
			body.WriteString(strings.TrimPrefix(line, indent))
			body.WriteByte('\n')

		case inOther:
			if m != nil && m[1][0] == opener[0] && len(m[1]) >= len(opener) && m[2] == "" {
				inOther = false
			}

		case m != nil:
			opener = m[1]
			lang := strings.ToLower(m[2])

			if lang == "yaml" || lang == "yml" {
				indent = line[:len(line)-len(strings.TrimLeft(line, " \t"))]
				current = &yamlBlock{line: lineNo}
			} else {
				inOther = true
			}
		}
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan document: %w", err)
	}

	if current != nil {
		return nil, fmt.Errorf("yaml block opened on line %d is never closed", current.line)
	}

	if inOther {
		return nil, fmt.Errorf("a fenced block opened with %q is never closed", opener)
	}

	return blocks, nil
}

// loadYAMLBlock writes body to its own mode-0600 file and loads it with
// config.Load, exactly as pmx reads its configuration. The mode matters,
// because a documented config that sets default_user_password is rejected
// when group or world can read it.
//
// config.Load decodes without validating a context, so a block that decodes
// could still teach a setting every command rejects. Each context in the
// block therefore also runs through the checks pmx context add and pmx
// context update apply before they save one: config.ApplyDefaults, then
// config.StrictValidateContext, and apiclient.ValidateJumpChain on a
// non-blank ssh.jump. Every message is reported, prefixed with the context's
// name.
func loadYAMLBlock(t *testing.T, body string) error {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	cfg, err := config.Load(path)
	if err != nil {
		return err
	}

	names := make([]string, 0, len(cfg.Contexts))
	for name := range cfg.Contexts {
		names = append(names, name)
	}

	sort.Strings(names)

	var problems []string

	for _, name := range names {
		ctx := cfg.Contexts[name]
		if ctx == nil {
			problems = append(problems, fmt.Sprintf("context %q: the entry is empty", name))

			continue
		}

		config.ApplyDefaults(ctx)

		for _, msg := range config.StrictValidateContext(ctx) {
			problems = append(problems, fmt.Sprintf("context %q: %s", name, msg))
		}

		if strings.TrimSpace(ctx.SSH.Jump) != "" {
			if err := apiclient.ValidateJumpChain(ctx.SSH.Jump); err != nil {
				problems = append(problems, fmt.Sprintf("context %q: ssh.jump %q: %v", name, ctx.SSH.Jump, err))
			}
		}
	}

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}

	return nil
}

// TestDocs_YAMLConfigBlocksLoad loads every fenced yaml block in the README
// and the configuration man page through loadYAMLBlock, so an example that is
// not valid YAML, that pmx cannot decode into its configuration, or that
// holds a context pmx would refuse to save fails the build instead of
// failing the operator who copies it.
func TestDocs_YAMLConfigBlocksLoad(t *testing.T) {
	for _, src := range yamlDocSources {
		t.Run(filepath.Base(src), func(t *testing.T) {
			raw, err := os.ReadFile(src)
			require.NoError(t, err)

			blocks, err := extractYAMLBlocks(string(raw))
			require.NoError(t, err)
			require.NotEmpty(t, blocks, "%s has no yaml blocks; the extractor or the document regressed", src)

			for _, b := range blocks {
				t.Run(fmt.Sprintf("line_%d", b.line), func(t *testing.T) {
					require.NoError(t, loadYAMLBlock(t, b.body),
						"%s: the yaml block opened on line %d does not load:\n%s", src, b.line, b.body)
				})
			}
		})
	}
}

// TestDocs_YAMLConfigBlocksLoad_RejectsBrokenExample proves the gate bites.
// A flow-mapped auth block with an unquoted reference is the mistake the
// gate exists to catch, because the "{" of the reference opens a nested
// mapping.
func TestDocs_YAMLConfigBlocksLoad_RejectsBrokenExample(t *testing.T) {
	doc := "Intro.\n\n```yaml\ncontexts:\n  lab:\n    host: pve1.example.com\n" +
		"    auth: {type: token, username: root@pam, token-id: ci, secret: ${TOK}}\n```\n"

	blocks, err := extractYAMLBlocks(doc)
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	require.Error(t, loadYAMLBlock(t, blocks[0].body))

	quoted := strings.Replace(blocks[0].body, "${TOK}", `"${TOK}"`, 1)
	require.NoError(t, loadYAMLBlock(t, quoted), "a quoted reference inside a flow mapping must load")
}

// TestDocs_YAMLConfigBlocksLoad_RejectsInvalidContext proves the gate goes
// past decoding. Each block below is valid YAML that config.Load accepts, and
// each holds a context that pmx context add would refuse to save.
func TestDocs_YAMLConfigBlocksLoad_RejectsInvalidContext(t *testing.T) {
	const head = "contexts:\n  lab:\n    host: pve1.example.com\n" +
		"    auth: {type: token, username: root@pam, token-id: ci, secret: s3cr3t}\n"

	for name, tc := range map[string]struct {
		extra string
		want  string
	}{
		"shell metacharacter in ssh.jump": {
			extra: "    ssh:\n      jump: 'a;rm -rf /'\n",
			want:  `context "lab": ssh.jump`,
		},
		"proxy port out of range": {
			extra: "    proxy:\n      url: https://proxy.example.com:99999\n",
			want:  `context "lab": proxy.url`,
		},
		"timeout that is not a duration": {
			extra: "    timeout:\n      connect: bogus\n",
			want:  `context "lab": timeout.connect`,
		},
		"unknown protocol": {
			extra: "    protocol: ftp\n",
			want:  `context "lab": protocol "ftp"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorContains(t, loadYAMLBlock(t, head+tc.extra), tc.want)
		})
	}

	require.NoError(t, loadYAMLBlock(t, head), "the valid base context must pass the gate")
}

func TestExtractYAMLBlocks(t *testing.T) {
	for name, tc := range map[string]struct {
		doc     string
		want    []yamlBlock
		wantErr string
	}{
		"yaml and yml blocks, other languages skipped": {
			doc:  "```bash\nls\n```\n\n```yaml\na: 1\n```\n\n```yml\nb: 2\n```\n\n```\nc: 3\n```\n",
			want: []yamlBlock{{line: 5, body: "a: 1\n"}, {line: 9, body: "b: 2\n"}},
		},
		"indented block in a list item": {
			doc:  "- item\n\n  ```yaml\n  a:\n    b: 1\n  ```\n",
			want: []yamlBlock{{line: 3, body: "a:\n  b: 1\n"}},
		},
		"shorter fence inside a longer one stays content": {
			doc:  "````yaml\na: |\n  ```\n  x\n  ```\n````\n",
			want: []yamlBlock{{line: 1, body: "a: |\n  ```\n  x\n  ```\n"}},
		},
		"yaml fence inside another block is not a block": {
			doc:  "````markdown\n```yaml\na: 1\n```\n````\n",
			want: nil,
		},
		"tilde fence": {
			doc:  "~~~yaml\na: 1\n~~~\n",
			want: []yamlBlock{{line: 1, body: "a: 1\n"}},
		},
		"unclosed yaml block": {
			doc:     "```yaml\na: 1\n",
			wantErr: "never closed",
		},
		"unclosed other block": {
			doc:     "```bash\nls\n",
			wantErr: "never closed",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := extractYAMLBlocks(tc.doc)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// normalizeSpace collapses every run of whitespace to one space, so a
// phrase check is immune to where the man page source wraps its lines.
func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// TestDocs_ConnectionReferenceContent keeps the connection settings
// documented. make check-docs proves only that generation does not crash,
// so this test stands in for a content gate on the phrases operators rely
// on, including four sentences that are fixed word for word.
func TestDocs_ConnectionReferenceContent(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("pages", "pmx-config.5.md"))
	require.NoError(t, err)

	text := normalizeSpace(string(page))

	for _, phrase := range []string{
		"proxy.url", "proxy.username", "proxy.password", "proxy.from-env",
		"timeout.connect", "timeout.tls-handshake", "timeout.request",
		"PMX_API_ENDPOINT", "PMX_API_JUMP", "PMX_API_PROXY", "PMX_API_CA_CERT", "PMX_API_FINGERPRINT",
		"PMX_API_CONNECT_TIMEOUT", "PMX_API_TLS_HANDSHAKE_TIMEOUT", "PMX_API_REQUEST_TIMEOUT",
		"Connection settings resolve flag > environment variable > context config > built-in default.",
		"ALL_PROXY", "ssh.exe", "ControlPersist", "per attempt", "WAYLAND_DISPLAY",
		"one failed attempt per context", "can still stall", "the lesser of one second and a quarter",
		"750 milliseconds", "for ten seconds", "ssh-agent", "%40", "survives", "--redirect-url",
		"pmx auth now verifies a context's tls.ca-cert the way every other command does, so an " +
			"authentication call against a server whose certificate chains only to a system root fails " +
			"where it used to succeed.",
		"pmx context validate --connect no longer honours HTTPS_PROXY on its own; set proxy.from-env: " +
			"true on the context, or pass --api-proxy-from-env, to route the probe through the proxy environment.",
		"pmx context validate --connect prints a VIA column between REACHABLE and PRODUCT, so a script " +
			"that reads the table by column position should use --output json instead.",
		"The API bastion hop runs with BatchMode=yes and without a terminal, so it cannot prompt for a " +
			"password, a second factor, or an unknown host key; run ssh <bastion> once to accept its host " +
			"key before the first API command.",
	} {
		require.Contains(t, text, phrase, "pmx-config.5.md must carry %q", phrase)
	}

	// Each flag is checked in the bold form its definition-list entry opens
	// with, because a bare "--api-proxy" would also match inside
	// "--api-proxy-from-env" and let the --api-proxy entry vanish unnoticed.
	for _, flag := range []string{
		"--api-endpoint", "--api-jump", "--api-proxy", "--api-proxy-from-env", "--api-ca-cert",
		"--api-fingerprint", "--api-connect-timeout", "--api-tls-handshake-timeout", "--api-request-timeout",
	} {
		require.Contains(t, text, "**"+flag+"**", "pmx-config.5.md must carry an entry for %s", flag)
	}

	require.NotContains(t, text, "cannot be overridden per invocation",
		"the ssh.jump entry must name --api-jump instead of denying a per-invocation override")

	sections, err := os.ReadFile(filepath.Join("pages", "root-sections.md"))
	require.NoError(t, err)

	for _, name := range []string{
		"PMX_API_ENDPOINT", "PMX_API_JUMP", "PMX_API_PROXY", "PMX_API_CA_CERT", "PMX_API_FINGERPRINT",
		"PMX_API_CONNECT_TIMEOUT", "PMX_API_TLS_HANDSHAKE_TIMEOUT", "PMX_API_REQUEST_TIMEOUT",
	} {
		require.Contains(t, string(sections), "**"+name+"**", "root-sections.md must list %s", name)
	}

	require.NotContains(t, string(sections), "PMX_API_PROXY_FROM_ENV",
		"--api-proxy-from-env has no environment variable, so none may be documented")
}
