package iamadmin

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/GuitarWag/gcp-local/internal/state"
)

// consoleError carries the HTTP status the console handler maps into a
// GCP-style error body via errors.As on interface{ StatusCode() int }.
type consoleError struct {
	status int
	msg    string
}

func (e *consoleError) Error() string   { return e.msg }
func (e *consoleError) StatusCode() int { return e.status }

func consoleErr(status int, msg string) error {
	return &consoleError{status: status, msg: msg}
}

func storeErr(err error) error {
	return consoleErr(http.StatusInternalServerError, err.Error())
}

// accountRow is the projection the IAM Admin page renders.
func accountRow(sa serviceAccount) map[string]any {
	return map[string]any{
		"name":        sa.Name,
		"email":       sa.Email,
		"projectId":   sa.ProjectID,
		"displayName": sa.DisplayName,
		"description": sa.Description,
		"uniqueId":    sa.UniqueID,
	}
}

// keyRow mirrors the REST serviceAccountKey projection.
func keyRow(k serviceAccountKey) map[string]any {
	return map[string]any{
		"keyId":           lastSegment(k.Name),
		"keyAlgorithm":    k.KeyAlgorithm,
		"keyType":         k.KeyType,
		"validAfterTime":  k.ValidAfterTime,
		"validBeforeTime": k.ValidBeforeTime,
	}
}

func lastSegment(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// resolveAccount maps an account email to its full store key. REST
// creates accounts under any project, so the email may live outside
// s.project; the email embeds the project and matches at most one record.
func (s *Service) resolveAccount(email string) (string, error) {
	direct := accountName(s.project, email)
	_, err := s.store.Get(NamespaceAccounts, direct)
	if err == nil {
		return direct, nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return "", storeErr(err)
	}
	all, err := s.store.List(NamespaceAccounts, "")
	if err != nil {
		return "", storeErr(err)
	}
	for k := range all {
		if strings.HasSuffix(k, "/serviceAccounts/"+email) {
			return k, nil
		}
	}
	return "", consoleErr(http.StatusNotFound, "service account not found")
}

// ConsoleAccounts lists every service account, sorted by email.
func (s *Service) ConsoleAccounts() ([]map[string]any, error) {
	all, err := s.store.List(NamespaceAccounts, "")
	if err != nil {
		return nil, storeErr(err)
	}
	out := make([]map[string]any, 0, len(all))
	for _, v := range all {
		var sa serviceAccount
		if json.Unmarshal(v, &sa) != nil {
			continue
		}
		out = append(out, accountRow(sa))
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i]["email"].(string)
		b, _ := out[j]["email"].(string)
		return a < b
	})
	return out, nil
}

// ConsoleCreateAccount reuses the REST create rules: same email shape,
// uniqueId and conflict check, minus the HTTP plumbing.
func (s *Service) ConsoleCreateAccount(accountID, displayName, description string) (map[string]any, error) {
	if accountID == "" {
		return nil, consoleErr(http.StatusBadRequest, "accountId required")
	}
	email := fmt.Sprintf("%s@%s.iam.gserviceaccount.com", accountID, s.project)
	name := accountName(s.project, email)
	_, err := s.store.Get(NamespaceAccounts, name)
	if err == nil {
		return nil, consoleErr(http.StatusConflict, fmt.Sprintf("service account %s already exists", email))
	}
	if !errors.Is(err, state.ErrNotFound) {
		return nil, storeErr(err)
	}
	sa := serviceAccount{
		Name:        name,
		ProjectID:   s.project,
		UniqueID:    randomID(21),
		Email:       email,
		DisplayName: displayName,
		Description: description,
	}
	data, err := json.Marshal(sa)
	if err != nil {
		return nil, storeErr(err)
	}
	if err := s.store.Put(NamespaceAccounts, name, data); err != nil {
		return nil, storeErr(err)
	}
	return accountRow(sa), nil
}

// ConsoleDeleteAccount mirrors the REST item DELETE arm: delete the
// account, then cascade-delete its keys.
func (s *Service) ConsoleDeleteAccount(email string) error {
	name, err := s.resolveAccount(email)
	if err != nil {
		return err
	}
	if err := s.store.Delete(NamespaceAccounts, name); err != nil {
		return consoleErr(http.StatusNotFound, "service account not found")
	}
	keyPrefix := name + "/keys/"
	all, _ := s.store.List(NamespaceKeys, keyPrefix)
	for k := range all {
		_ = s.store.Delete(NamespaceKeys, k)
	}
	return nil
}

// ConsoleKeys lists the account's keys as the REST projection renders them.
func (s *Service) ConsoleKeys(account string) ([]map[string]any, error) {
	name, err := s.resolveAccount(account)
	if err != nil {
		return nil, err
	}
	all, err := s.store.List(NamespaceKeys, name+"/keys/")
	if err != nil {
		return nil, storeErr(err)
	}
	out := make([]map[string]any, 0, len(all))
	for _, v := range all {
		var k serviceAccountKey
		if json.Unmarshal(v, &k) != nil {
			continue
		}
		out = append(out, keyRow(k))
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i]["keyId"].(string)
		b, _ := out[j]["keyId"].(string)
		return a < b
	})
	return out, nil
}

// ConsoleCreateKey mints a key exactly as the REST keys collection POST
// does: randomID(40) id, fake credentials payload, 10-year window.
func (s *Service) ConsoleCreateKey(account string) (map[string]any, error) {
	name, err := s.resolveAccount(account)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	keyID := randomID(40)
	key := serviceAccountKey{
		Name:            name + "/keys/" + keyID,
		PrivateKeyType:  "TYPE_GOOGLE_CREDENTIALS_FILE",
		KeyAlgorithm:    "KEY_ALG_RSA_2048",
		PrivateKeyData:  base64.StdEncoding.EncodeToString([]byte(fakeCredJSON(lastSegment(name), keyID))),
		ValidAfterTime:  now.Format(time.RFC3339),
		ValidBeforeTime: now.AddDate(10, 0, 0).Format(time.RFC3339),
		KeyOrigin:       "GOOGLE_PROVIDED",
		KeyType:         "USER_MANAGED",
	}
	data, err := json.Marshal(key)
	if err != nil {
		return nil, storeErr(err)
	}
	if err := s.store.Put(NamespaceKeys, key.Name, data); err != nil {
		return nil, storeErr(err)
	}
	return keyRow(key), nil
}

// ConsoleDeleteKey deletes one stored key; account and key must exist.
func (s *Service) ConsoleDeleteKey(account, keyID string) error {
	name, err := s.resolveAccount(account)
	if err != nil {
		return err
	}
	if err := s.store.Delete(NamespaceKeys, name+"/keys/"+keyID); err != nil {
		return consoleErr(http.StatusNotFound, "key not found")
	}
	return nil
}

// ConsoleCredentials returns the decoded credentials file for a key and
// the download filename the console offers.
func (s *Service) ConsoleCredentials(account, keyID string) ([]byte, string, error) {
	name, err := s.resolveAccount(account)
	if err != nil {
		return nil, "", err
	}
	data, err := s.store.Get(NamespaceKeys, name+"/keys/"+keyID)
	if err != nil {
		return nil, "", consoleErr(http.StatusNotFound, "key not found")
	}
	var k serviceAccountKey
	if err := json.Unmarshal(data, &k); err != nil {
		return nil, "", storeErr(err)
	}
	raw, err := base64.StdEncoding.DecodeString(k.PrivateKeyData)
	if err != nil {
		return nil, "", consoleErr(http.StatusInternalServerError, fmt.Sprintf("privateKeyData not base64: %v", err))
	}
	return raw, fmt.Sprintf("%s-%s.json", account, keyID), nil
}
