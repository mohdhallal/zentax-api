package types

type Exposure string

type exposures struct {
	External Exposure
	Internal Exposure
	Both     Exposure
}

var Exposures = exposures{
	External: "external",
	Internal: "internal",
	Both:     "both",
}
