package parser

// AudioLabels lists the audio labels the parser gives a release ("Atmos", "TrueHD", "DDP"…),
// in the order they're checked. The quality builder offers them as custom-format values, so
// a condition can only be built from something a release name can actually produce.
func AudioLabels() []string {
	out := make([]string, len(audioTags))
	for i, t := range audioTags {
		out[i] = t.label
	}
	return out
}

// StreamingServices lists the streaming-service tags the parser reads into
// Release.Streaming ("AMZN", "NF", "ATVP"…), as it writes them, in the order they're checked.
func StreamingServices() []string {
	out := make([]string, len(streamingServices))
	for i, s := range streamingServices {
		out[i] = s.tag
	}
	return out
}
