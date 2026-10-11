package config

import (
	"net"
	"net/mail"
	"net/url"
	"strings"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
)

// NewSECSource assembles the v2.5.3 SEC 8-K source after the same licence
// checks as RSS. SEC requires a declared operator identity ending in a contact
// address; the value comes only from the private canonical configuration.
func NewSECSource(r SECRegistration, sources map[string]d.SourceRegistration, userAgent string, timeout time.Duration, maxInputBytes int, fixtureOnly bool, now func() time.Time) (*monitoring.SECFilingSource, error) {
	policy, ok := sources[r.SourceID]
	if !ok || !policy.Enabled || !policy.LicenceVerified || !policy.AllowAnalysis || policy.LicenceRef != r.LicenceRef {
		return nil, d.Fail("SOURCE_LICENCE_REQUIRED", 403)
	}
	fields := strings.Fields(userAgent)
	if len(fields) < 2 {
		return nil, d.Fail("SEC_IDENTITY_REQUIRED", 503)
	}
	if address, err := mail.ParseAddress(fields[len(fields)-1]); err != nil || address.Address != fields[len(fields)-1] {
		return nil, d.Fail("SEC_IDENTITY_REQUIRED", 503)
	}
	if r.LookbackSeconds <= 0 || r.LookbackSeconds > int64(366*24*time.Hour/time.Second) || r.MinIntervalMS <= 0 || r.MaxMetadataBytes <= 0 {
		return nil, d.Fail("SOURCE_CONFIGURATION_REQUIRED", 503)
	}
	dataBase, archiveBase, hosts := monitoring.SECDataBase, monitoring.SECArchiveBase, []string{"data.sec.gov", "www.sec.gov"}
	if r.FixtureBase != "" {
		u, err := url.Parse(r.FixtureBase)
		if !fixtureOnly || err != nil || u.Scheme != "http" || u.Path != "" || u.RawQuery != "" || u.User != nil {
			return nil, d.Fail("SEC_FIXTURE_NOT_ADMITTED", 403)
		}
		ip := net.ParseIP(u.Hostname())
		if ip == nil || !ip.IsLoopback() {
			return nil, d.Fail("SEC_FIXTURE_NOT_ADMITTED", 403)
		}
		dataBase, archiveBase, hosts = r.FixtureBase, r.FixtureBase, []string{u.Hostname()}
	}
	limit := maxInputBytes
	if r.MaxMetadataBytes > limit {
		limit = r.MaxMetadataBytes
	}
	fetcher, err := monitoring.NewFetcher(monitoring.FetchPolicy{Hosts: hosts, Timeout: timeout, MaxBytes: limit, FixtureOnly: fixtureOnly, UserAgent: userAgent})
	if err != nil {
		return nil, err
	}
	return &monitoring.SECFilingSource{Fetcher: fetcher, SourceID: r.SourceID, LicenceRef: r.LicenceRef, Companies: r.Companies, Items: r.Items, Lookback: time.Duration(r.LookbackSeconds) * time.Second,
		MaxFilings: r.MaxFilings, MaxMetadataBytes: r.MaxMetadataBytes, MaxExhibitBytes: maxInputBytes, MinInterval: time.Duration(r.MinIntervalMS) * time.Millisecond, DataBase: dataBase, ArchiveBase: archiveBase, Clock: now}, nil
}
