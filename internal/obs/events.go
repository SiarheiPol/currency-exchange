package obs

// Ev* constants are the canonical event names used as structured log message
// keys throughout the service. All values are lower-case, space-separated
// strings. New event names must be added here before use.
const (
	EvHTTPRequestReceived  = "http request received"
	EvHTTPRequestCompleted = "http request completed"
	EvJobReserved          = "job reserved"
	EvJobCompleted         = "job completed"
	EvJobRescheduled       = "job rescheduled"
	EvJobFailed            = "job failed"
	EvSchedulerTick        = "scheduler tick"
	EvUpstreamCallStarted  = "upstream call started"
	EvUpstreamCallFinished = "upstream call finished"
	EvCoalescingCollapsed  = "coalescing collapsed"
	EvWorkerOpFailed       = "worker operation failed"
)
