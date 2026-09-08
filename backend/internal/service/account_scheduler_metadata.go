package service

const AccountSchedulerMetadataVersion = 1

// AccountSchedulerMetadata carries the identity digest computed before the
// scheduler strips non-scheduling credential/extra fields. It is only a
// candidate-screening hint; forwarding still reloads and verifies the complete
// account from the repository.
type AccountSchedulerMetadata struct {
	Version             int    `json:"version"`
	IdentityFingerprint string `json:"identity_fingerprint"`
}

func ValidSchedulerMetadataIdentity(account *Account) bool {
	if account == nil || account.SchedulerMetadata == nil || account.SchedulerMetadata.Version != AccountSchedulerMetadataVersion {
		return false
	}
	fingerprint := account.SchedulerMetadata.IdentityFingerprint
	if len(fingerprint) != 64 {
		return false
	}
	for _, char := range fingerprint {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func managedModelSchedulingFingerprint(account *Account) string {
	if account == nil {
		return ""
	}
	if account.SchedulerMetadata != nil {
		if !ValidSchedulerMetadataIdentity(account) {
			return ""
		}
		return account.SchedulerMetadata.IdentityFingerprint
	}
	return ManagedModelAccountFingerprint(account)
}
