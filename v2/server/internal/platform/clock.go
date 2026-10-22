package platform

import "time"

// Now is replaceable in tests without changing domain rules.
var Now = time.Now
