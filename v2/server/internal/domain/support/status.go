package support

var validStatuses = map[string]bool{"open": true, "reviewing": true, "resolved": true, "rejected": true}

func ValidStatus(s string) bool { return validStatuses[s] }
