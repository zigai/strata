package stratacobra

// Test-only access to the synchronization step that [WithFlags] runs, so tests
// can exercise it without a full load.
var (
	SyncFlagsToStruct = syncFlagsToStruct
	WithMetadata      = withMetadata
)
