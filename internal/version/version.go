package version

// Version is the release line (MAJOR.MINOR.0). The patch is claimed by the
// release workflow (woodpecker-plugin-release-versions mode version); never set
// it by hand.
const Version = "0.1.0"

// ReleaseTag is set at image build time via -ldflags to the released version.
var ReleaseTag = Version
