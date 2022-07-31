package config

// Variant is the OS / Architecture combination that Grafana can be compiled for.
type Variant string

const (
	VariantLinuxAmd64     Variant = "linux-amd64"
	VariantLinuxAmd64Musl Variant = "linux-amd64-musl"
	VariantArmV6          Variant = "linux-armv6"
	VariantArmV7          Variant = "linux-armv7"
	VariantArmV7Musl      Variant = "linux-armv7-musl"
	VariantArm64          Variant = "linux-arm64"
	VariantArm64Musl      Variant = "linux-arm64-musl"
	VariantDarwinAmd64    Variant = "darwin-amd64"
	VariantWindowsAmd64   Variant = "windows-amd64"
)

// Architecture is an allowed value in the GOARCH environment variable.
type Architecture string

const (
	ArchAMD64 Architecture = "amd64"
	ArchARMv6 Architecture = "armv6"
	ArchARM64 Architecture = "arm64"
	ArchARMHF Architecture = "armhf"
	ArchARM   Architecture = "arm"
)

type OS string

const (
	OSWindows OS = "windows"
	OSDarwin  OS = "darwin"
	OSLinux   OS = "linux"
)

type LibC string

const (
	LibCMusl = "musl"
)
