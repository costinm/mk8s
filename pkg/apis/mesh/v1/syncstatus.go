package v1

// SyncStatus tracks the sync status for K8S or other revisioned resource.
//
// This is an alternative to an informer holding the entire data set in memory and loading
// all data at startup.
//
// A client would load the sync status and list or watch based on last resource that was handled.
type SyncStatus struct {
}
