package config

import (
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
	"github.com/pelletier/go-toml/v2"
)

// StorageAdmin is the administrative connection used only by instance-cli to
// provision storage. No worker or API reads this section.
type StorageAdmin struct {
	Environment      string `toml:"environment"`
	InstanceID       string `toml:"instance_id"`
	AdminDatabaseURL string `toml:"admin_database_url"`
}

func (c StorageAdmin) Binding() d.Binding {
	return d.Binding{InstanceID: c.InstanceID, Environment: c.Environment}
}

func LoadStorageAdmin(path string) (StorageAdmin, error) {
	var file struct {
		Application struct {
			Storage StorageAdmin `toml:"storage"`
		} `toml:"application"`
	}
	raw, err := privateFile(path, 1<<20)
	if err != nil {
		return StorageAdmin{}, err
	}
	if toml.Unmarshal(raw, &file) != nil {
		return StorageAdmin{}, d.Fail("STORAGE_CONFIG_INVALID", 503)
	}
	c := file.Application.Storage
	if !c.Binding().Valid() || c.AdminDatabaseURL == "" {
		return StorageAdmin{}, d.Fail("STORAGE_CONFIG_REQUIRED", 503)
	}
	return c, nil
}
