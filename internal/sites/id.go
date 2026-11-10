package sites

import "crypto/rand"

func randomID() string { return rand.Text() }
