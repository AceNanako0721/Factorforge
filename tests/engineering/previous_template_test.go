package engineering_test

import (
	guard "github.com/AceNanako0721/Factorforge/tools/repoguard"
)

// previousTemplate is the config template of the previous release; history
// scans accept it and everything derived from it, the current scan does not.
func previousTemplate() ([]byte, error) { return []byte(guard.PreviousConfigTemplate()), nil }
