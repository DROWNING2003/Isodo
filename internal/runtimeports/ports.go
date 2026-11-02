// Package runtimeports defines Guest-owned service ports, not host API listeners.
package runtimeports

const (
	GuestAgent  = 31024
	GuestEgress = 31080
	Workbench   = 38080
)

// Keep legacy runtime ports closed to generic preview during coordinated
// Node/Guest/image upgrades. They are no longer bound by the new runtime.
func ReservedHTTP(port uint32) bool {
	switch port {
	case GuestAgent, GuestEgress, Workbench, 1024, 1080, 8080:
		return true
	}
	return false
}
