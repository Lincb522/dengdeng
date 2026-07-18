package version

// These values are filled by release and server-side builds through -ldflags.
// Development builds intentionally retain recognizable fallback values.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = ""
)

type BuildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
}

func Info() BuildInfo {
	return BuildInfo{Version: Version, Commit: Commit, BuildTime: BuildTime}
}
