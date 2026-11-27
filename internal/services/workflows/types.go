package workflows

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Workflow mirrors the google.cloud.workflows.v1.Workflow resource on the
// REST wire. sourceContents is the raw YAML source (plain string, limit
// 128KB), not base64.
type Workflow struct {
	Name           string            `json:"name"`
	State          string            `json:"state"`
	SourceContents string            `json:"sourceContents,omitempty"`
	Description    string            `json:"description,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	CallLogLevel   string            `json:"callLogLevel,omitempty"`
	CreateTime     time.Time         `json:"createTime"`
	UpdateTime     time.Time         `json:"updateTime"`
	RevisionID     string            `json:"revisionId"`
}

// Resource-name and validation helpers. Real workflow IDs must start with
// a letter, contain only letters, digits, underscores and hyphens, be 1-64
// chars long, and end with a letter or number.
const maxWorkflowIDLen = 64

var (
	// ErrInvalidID reports a workflowId that breaks the API rules.
	ErrInvalidID = errors.New("workflowId must start with a letter, contain only letters, digits, underscores and hyphens, be 1-64 characters long, and end with a letter or number")
	// ErrSourceTooLarge reports a sourceContents over the 128KB limit.
	ErrSourceTooLarge = errors.New("sourceContents exceeds the 128KB limit")
)

// ParseWorkflowName splits a full resource name
// projects/{p}/locations/{l}/workflows/{w} into its project, location and
// workflow id parts.
func ParseWorkflowName(name string) (project, location, id string, err error) {
	segs := strings.Split(name, "/")
	if len(segs) != 6 || segs[0] != "projects" || segs[2] != "locations" || segs[4] != "workflows" {
		return "", "", "", fmt.Errorf("invalid workflow resource name %q", name)
	}
	return segs[1], segs[3], segs[5], nil
}

// ValidateWorkflowID reports whether id satisfies the real workflowId
// rules: starts with a letter, only letters/digits/underscores/hyphens,
// 1-64 chars, ends with a letter or number.
func ValidateWorkflowID(id string) bool {
	if len(id) < 1 || len(id) > maxWorkflowIDLen {
		return false
	}
	if !isLetter(id[0]) || !isAlnum(id[len(id)-1]) {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !isLetter(c) && !isDigit(c) && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func isLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isDigit(c byte) bool  { return c >= '0' && c <= '9' }
func isAlnum(c byte) bool  { return isLetter(c) || isDigit(c) }

// NewRevisionID mints a revision id in the real format "NNNNNN-xxx":
// six digits, a hyphen, then three lowercase alphanumerics.
func NewRevisionID(seq int) string {
	if seq < 0 {
		seq = 0
	}
	if seq > 999999 {
		seq = 999999
	}
	return fmt.Sprintf("%06d-%s", seq, randomSuffix(3))
}

// randomSuffix returns n lowercase alphanumeric characters.
func randomSuffix(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand never fails on the supported platforms; fall back to
		// a fixed suffix so the format stays valid.
		return "000"
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	for i, b := range buf {
		buf[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(buf)
}
