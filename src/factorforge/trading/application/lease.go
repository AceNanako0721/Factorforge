package application

import (
	"strconv"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
)

// AcquireExecutorLease is how a starting executor takes the run's lease. It
// records the executor's process and, when the previous holder's lease has
// expired and proof shows its process has stopped, isolates that epoch and takes
// over in the same transaction, leaving the run in RECOVERY_CHECK. The evidence
// goes into the audit trail. Without such proof the previous holder still has
// to be fenced by an operator (POST /executors/fence).
func AcquireExecutorLease(run *d.Aggregate, holder, instance string, now time.Time, duration time.Duration, proof d.ProcessProof) (int64, error) {
	epoch, evidence, err := run.AcquireLeaseAs(holder, instance, now, duration, proof)
	if err == nil && evidence != "" {
		principal := d.Principal{PrincipalID: holder, Environment: run.RunKey.Environment, AccountID: run.RunKey.AccountID}
		Audit(run, "EXECUTOR_TAKEOVER", principal, "takeover:"+holder+":"+strconv.FormatInt(epoch, 10), Response{"epoch": epoch, "holder": holder, "evidence": evidence})
	}
	return epoch, err
}
