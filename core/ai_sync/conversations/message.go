package conversations

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"

	maxMessageBytes = 16 * 1024
	maxTitleBytes   = 512
	maxProjectBytes = 128

	digestIDLength = 64
)

// Message is one public, user-visible conversation message. The ID is derived
// from the source thread, turn, item, role, and public text only, so editing a
// message produces a new ID while unrelated metadata changes do not.
//
// ContextOnly marks a message that only supplies background for follow-up
// messages. It can inform updates to existing cards but must not be cited as
// evidence for a new card.
type Message struct {
	ID               string `json:"id"`
	ThreadID         string `json:"threadId"`
	ThreadTitle      string `json:"threadTitle"`
	Project          string `json:"project"`
	Role             string `json:"role"`
	Text             string `json:"text"`
	ContextOnly      bool   `json:"contextOnly,omitempty"`
	RegistrationOnly bool   `json:"-"` // Manual upgrade pass for registration details; do not replay ordinary task changes.
	At               int64  `json:"at"`
}

// injectedTextBlocks are client-side envelopes and UI metadata that the CLIs
// prepend to user turns. They are not part of the user's public text.
var injectedTextBlocks = []*regexp.Regexp{
	regexp.MustCompile(`(?is)<environment_context\b[^>]*>.*?</environment_context>`),
	regexp.MustCompile(`(?is)<user_instructions\b[^>]*>.*?</user_instructions>`),
	regexp.MustCompile(`(?is)<instructions\b[^>]*>.*?</instructions>`),
	regexp.MustCompile(`(?is)<system-reminder\b[^>]*>.*?</system-reminder>`),
	regexp.MustCompile(`(?is)<system\b[^>]*>.*?</system>`),
	regexp.MustCompile(`(?is)<turn_aborted\b[^>]*>.*?</turn_aborted>`),
	regexp.MustCompile(`(?is)<command-name\b[^>]*>.*?</command-name>`),
	regexp.MustCompile(`(?is)<command-message\b[^>]*>.*?</command-message>`),
	regexp.MustCompile(`(?is)<command-args\b[^>]*>.*?</command-args>`),
	regexp.MustCompile(`(?is)<local-command-stdout\b[^>]*>.*?</local-command-stdout>`),
	regexp.MustCompile(`(?is)<local-command-stderr\b[^>]*>.*?</local-command-stderr>`),
	regexp.MustCompile(`(?is)<bash-input\b[^>]*>.*?</bash-input>`),
	regexp.MustCompile(`(?is)<bash-stdout\b[^>]*>.*?</bash-stdout>`),
	regexp.MustCompile(`(?is)<bash-stderr\b[^>]*>.*?</bash-stderr>`),
	regexp.MustCompile(`(?is)<collaboration_mode\b[^>]*>.*?</collaboration_mode>`),
	regexp.MustCompile(`(?is)<collaboration-mode\b[^>]*>.*?</collaboration-mode>`),
	regexp.MustCompile(`(?is)<permissions\b[^>]*>.*?</permissions>`),
	regexp.MustCompile(`(?is)<ide_context\b[^>]*>.*?</ide_context>`),
	regexp.MustCompile(`(?is)<ide_opened_file\b[^>]*>.*?</ide_opened_file>`),
	regexp.MustCompile(`(?is)<open_and_recently_viewed_files\b[^>]*>.*?</open_and_recently_viewed_files>`),
	regexp.MustCompile(`(?is)<user_shell_command\b[^>]*>.*?</user_shell_command>`),
	regexp.MustCompile(`(?is)<repo_context\b[^>]*>.*?</repo_context>`),
	regexp.MustCompile(`(?is)<current_date\b[^>]*>.*?</current_date>`),
	regexp.MustCompile(`(?is)<timezone\b[^>]*>.*?</timezone>`),
	regexp.MustCompile(`(?is)<model_context\b[^>]*>.*?</model_context>`),
	regexp.MustCompile(`(?is)<plan_mode\b[^>]*>.*?</plan_mode>`),
}

// injectedTag is used to drop leftover wrappers when an envelope was cut off
// before its closing tag.
var injectedTag = regexp.MustCompile(`(?i)</?(?:environment_context|user_instructions|instructions|system-reminder|system|turn_aborted|command-name|command-message|command-args|local-command-stdout|local-command-stderr|bash-input|bash-stdout|bash-stderr|collaboration_mode|collaboration-mode|permissions|ide_context|ide_opened_file|open_and_recently_viewed_files|user_shell_command|repo_context|current_date|timezone|model_context|plan_mode)(?:\b[^>]*)?>`)

type secretPattern struct {
	pattern     *regexp.Regexp
	replacement string
}

// publicSecretPatterns cover well-known API key prefixes and credential
// assignments. They intentionally stay conservative so ordinary prose and
// identifiers are not redacted.
var publicSecretPatterns = []secretPattern{
	{
		pattern:     regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`),
		replacement: "[redacted private key]",
	},
	{
		pattern:     regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),
		replacement: "[redacted]",
	},
	{
		pattern:     regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`),
		replacement: "[redacted]",
	},
	{
		pattern:     regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}\b`),
		replacement: "[redacted]",
	},
	{
		pattern:     regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`),
		replacement: "[redacted]",
	},
	{
		pattern:     regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}\b`),
		replacement: "[redacted]",
	},
	{
		pattern:     regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`),
		replacement: "[redacted]",
	},
	{
		pattern:     regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/-]{16,}=*`),
		replacement: "Bearer [redacted]",
	},
	{
		pattern:     regexp.MustCompile(`(?i)\b(OPENAI_API_KEY|ANTHROPIC_API_KEY|GITHUB_TOKEN|AWS_SECRET_ACCESS_KEY|API_KEY|ACCESS_TOKEN|SECRET_KEY|CLIENT_SECRET)\b\s*[:=]\s*['"]?[^\s'"]{8,}['"]?`),
		replacement: "${1}=[redacted]",
	},
}

var (
	agentsInstructionPrefix = regexp.MustCompile(`(?is)^\s*#\s+AGENTS\.md instructions for [^\n]*\n.*?</INSTRUCTIONS>\s*`)
	agentsInstructionHeader = regexp.MustCompile(`(?im)^\s*#\s+AGENTS\.md instructions for [^\n]*\n?`)
)

func messageDigest(threadID, turnID, itemID, role, text string) string {
	digest := sha256.New()
	// Domain separation keeps this digest distinct from other catendar hashes.
	io.WriteString(digest, "catendar-conversation-message-v1\x00")
	for _, part := range []string{threadID, turnID, itemID, role, text} {
		io.WriteString(digest, part)
		digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func cleanPublicText(value string) string {
	value = strings.ToValidUTF8(value, "")
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	value = strings.TrimSpace(value)
	value = agentsInstructionPrefix.ReplaceAllString(value, "")
	value = stripInjectedMetadata(value)
	value = agentsInstructionHeader.ReplaceAllString(value, "")
	value = redactPublicSecrets(value)
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value)
	return strings.TrimSpace(value)
}

func cleanAssistantText(value string) string {
	value = strings.ToValidUTF8(value, "")
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	value = stripInjectedMetadata(value)
	value = redactPublicSecrets(value)
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value)
	return strings.TrimSpace(value)
}

func stripInjectedMetadata(value string) string {
	for _, pattern := range injectedTextBlocks {
		value = pattern.ReplaceAllString(value, "")
	}
	return injectedTag.ReplaceAllString(value, "")
}

func redactPublicSecrets(value string) string {
	for _, rule := range publicSecretPatterns {
		value = rule.pattern.ReplaceAllString(value, rule.replacement)
	}
	return value
}

func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func cleanThreadTitle(value string) string {
	value = strings.ToValidUTF8(value, "")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.Join(strings.Fields(value), " ")
	value = redactPublicSecrets(value)
	value = truncateUTF8(value, maxTitleBytes)
	if value == "" {
		return "Untitled conversation"
	}
	return value
}

func safeProject(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	cwd = strings.TrimRight(cwd, `/\`)
	if index := strings.LastIndexAny(cwd, `/\`); index >= 0 {
		cwd = cwd[index+1:]
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" || cwd == "." || cwd == ".." {
		return ""
	}
	cwd = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, cwd)
	cwd = redactPublicSecrets(cwd)
	return truncateUTF8(strings.TrimSpace(cwd), maxProjectBytes)
}
