package sshcmd

import "strings"

// valueOpts lists the single-letter ssh(1) options that consume a following
// value, taken from OpenSSH's ssh.c getopt string "46ab:c:e:fgi:kl:m:no:p:qstvx
// ACD:E:F:GI:J:KL:MNO:PQ:R:S:TVw:W:XYy" (portable OpenSSH, options.c/ssh.c).
// Letters here are exactly those followed by ':' in that string.
const valueOpts = "bcDEeFIiJLlmOopQRSWwB"

// SplitPassthrough splits the argv remainder that follows the ssh destination
// (node/user@host) into leading ssh options and a trailing remote command.
//
// ssh's own getopt does not permute argv on every platform (notably BSD/macOS),
// so options appearing after the destination must be moved back in front of it
// by the caller; SplitPassthrough performs the classification that makes that
// reordering possible.
//
// Rules:
//   - a bare "--" ends option scanning; the "--" itself is dropped and every
//     token after it is the remote command
//   - "-X" where X takes a value (valueOpts) consumes the next token as that
//     option's value, even if the next token itself starts with "-"
//   - a clustered token "-XYZ": scanned left to right, the first character
//     found in valueOpts decides the token's fate — if it has trailing
//     characters the rest of the token is its (attached) value and the whole
//     token stands alone; if it is the last character, the whole token
//     consumes the following argv entry as its value; if no character in the
//     cluster is in valueOpts, the token is a standalone option
//   - a "--long" token is passed through as an option verbatim; ssh rejects
//     unknown long options itself
//   - the first token that does not start with "-" begins the remote command;
//     it and everything after it (including further dash-prefixed tokens or a
//     literal "--") are passed through to command verbatim
func SplitPassthrough(args []string) (opts, command []string) {
	i := 0
	for i < len(args) {
		tok := args[i]

		if tok == "--" {
			i++
			break
		}

		if !strings.HasPrefix(tok, "-") {
			break
		}

		if strings.HasPrefix(tok, "--") {
			opts = append(opts, tok)
			i++
			continue
		}

		consumesNext := false
		body := tok[1:]
		for j := 0; j < len(body); j++ {
			if strings.IndexByte(valueOpts, body[j]) < 0 {
				continue
			}
			// First value-taking char in the cluster decides the token's
			// fate: trailing characters are an attached value (token stands
			// alone); a value-taking char in final position means the next
			// argv entry is the value.
			if j == len(body)-1 {
				consumesNext = true
			}
			break
		}

		opts = append(opts, tok)
		i++
		if consumesNext && i < len(args) {
			opts = append(opts, args[i])
			i++
		}
	}

	command = args[i:]
	return opts, command
}
