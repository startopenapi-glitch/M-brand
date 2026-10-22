package payments

// ValidIdentity checks the channel identity snapshot. Empty expected values
// mean the local configuration did not pin that dimension.
func ValidIdentity(mchid, appid, wantMchid, wantAppid, txid string) bool {
	if txid == "" {
		return false
	}
	if wantMchid != "" && mchid != wantMchid {
		return false
	}
	if wantAppid != "" && appid != wantAppid {
		return false
	}
	return true
}
