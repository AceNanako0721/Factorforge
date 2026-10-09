package config

import (
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"os"
	"path/filepath"
	"strings"
)

type ProviderBudgetRegistration struct {
	Policy d.ProviderPoolPolicy `json:"policy"`
	Grants []d.ProviderGrant    `json:"grants"`
}

func LoadProviderBudgetRegistration(root, path string, maxBytes int) (ProviderBudgetRegistration, error) {
	var result ProviderBudgetRegistration
	runtimeRoot := filepath.Join(root, "runtime")
	rel, err := filepath.Rel(runtimeRoot, path)
	if !filepath.IsAbs(root) || !filepath.IsAbs(path) || err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return result, d.Fail("PROVIDER_REGISTRATION_INVALID", 422)
	}
	// Reject a linked ancestor as well as a linked leaf, before opening secrets.
	for cursor := filepath.Clean(path); ; cursor = filepath.Dir(cursor) {
		info, e := os.Lstat(cursor)
		if e != nil || info.Mode()&os.ModeSymlink != 0 {
			return result, d.Fail("PROVIDER_REGISTRATION_INVALID", 422)
		}
		if cursor == filepath.Clean(root) {
			break
		}
		if filepath.Dir(cursor) == cursor {
			return result, d.Fail("PROVIDER_REGISTRATION_INVALID", 422)
		}
	}
	raw, err := privateFile(path, maxBytes)
	if err != nil {
		return result, err
	}
	if d.DecodePrivate(raw, &result) != nil || len(result.Grants) == 0 {
		return result, d.Fail("PROVIDER_REGISTRATION_INVALID", 422)
	}
	if err = result.Policy.Validate(); err != nil {
		return result, err
	}
	seen := map[string]bool{}
	for _, grant := range result.Grants {
		if !grant.Valid() || grant.PoolID != result.Policy.PoolID || seen[grant.Login] {
			return result, d.Fail("PROVIDER_REGISTRATION_INVALID", 422)
		}
		seen[grant.Login] = true
	}
	return result, nil
}
