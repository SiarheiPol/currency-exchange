package obs

// Ev* constants are the canonical event names used as structured log message
// keys throughout the service. All values are lower-case, space-separated
// strings. New event names must be added here before use.
const (
	EvHTTPRequestReceived         = "http request received"
	EvHTTPRequestCompleted        = "http request completed"
	EvJobReserved                 = "job reserved"
	EvJobCompleted                = "job completed"
	EvJobRescheduled              = "job rescheduled"
	EvJobFailed                   = "job failed"
	EvSchedulerTick               = "scheduler tick"
	EvUpstreamCallStarted         = "upstream call started"
	EvUpstreamCallFinished        = "upstream call finished"
	EvCoalescingCollapsed         = "coalescing collapsed"
	EvWorkerOpFailed              = "worker operation failed"
	EvProviderResponseAnomaly     = "provider response anomaly"
	EvProviderQuotaExceeded       = "provider quota exceeded"
	EvStartupProbeOK              = "startup probe ok"
	EvSchedulerStarted            = "scheduler starting"
	EvSchedulerExitedUnexpectedly = "scheduler exited unexpectedly"
	EvSchedulerStopTimeout        = "scheduler stop timeout"
	EvFakeproviderStarted         = "fakeprovider starting"
	EvShutdownSignalReceived      = "shutdown signal received"
	EvPanicRecovered              = "panic recovered"
	EvOpenAPIResponseInvalid      = "openapi response invalid"
	EvOpenAPIValidateEnabled      = "openapi validate middleware enabled"
	EvPostgresConnected           = "postgres connected"
	EvWorkerStarted               = "worker starting"
	EvWorkerExitedUnexpectedly    = "worker exited unexpectedly"
	EvHTTPServerStarted           = "http server starting"
	EvHTTPShutdownFailed          = "http shutdown failed"
	EvWorkerStopTimeout           = "worker did not stop within 10s"
	EvWorkerConfigDerived         = "derived worker config"
	EvFakeproviderConfig          = "fake provider config"
)
