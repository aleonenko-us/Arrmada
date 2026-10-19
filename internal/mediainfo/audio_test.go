package mediainfo

import "testing"

// Canned ffprobe output (AudioProbeArgs) for the shapes music cares about. No media file is
// read here.
func TestParseAudio(t *testing.T) {
	cases := []struct {
		name, json   string
		codec        string
		kbps, bits   int
		rate         int
		formatBitBps int
	}{
		{"FLAC 24/96", `{"streams":[{"index":0,"codec_name":"flac","codec_type":"audio","sample_fmt":"s32","sample_rate":"96000","channels":2,"bits_per_raw_sample":"24"}],
			"format":{"duration":"212.4","bit_rate":"2843123","tags":{"ARTIST":"Someone","title":"Song"}}}`,
			"flac", 2843, 24, 96000, 2843123},
		{"MP3 CBR 320", `{"streams":[{"codec_name":"mp3","codec_type":"audio","sample_fmt":"fltp","sample_rate":"44100","channels":2,"bit_rate":"320000"}],
			"format":{"duration":"180.0","bit_rate":"320412"}}`,
			"mp3", 320, 0, 44100, 320412},
		{"MP3 V0", `{"streams":[{"codec_name":"mp3","codec_type":"audio","sample_fmt":"fltp","sample_rate":"44100","channels":2,"bit_rate":"245378"}],
			"format":{"duration":"180.0","bit_rate":"246001"}}`,
			"mp3", 245, 0, 44100, 246001},
		{"AAC 256", `{"streams":[{"codec_name":"aac","codec_type":"audio","sample_fmt":"fltp","sample_rate":"44100","channels":2,"bit_rate":"256000"}],
			"format":{"duration":"200.0","bit_rate":"259000"}}`,
			"aac", 256, 0, 44100, 259000},
		{"WAV", `{"streams":[{"codec_name":"pcm_s16le","codec_type":"audio","sample_fmt":"s16","sample_rate":"44100","channels":2,"bits_per_sample":16,"bit_rate":"1411200"}],
			"format":{"duration":"10.0","bit_rate":"1411301"}}`,
			"pcm_s16le", 1411, 16, 44100, 1411301},
	}
	for _, c := range cases {
		info, err := ParseAudio([]byte(c.json))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if info.Codec != c.codec || info.BitrateKbps != c.kbps || info.BitsPerSample != c.bits ||
			info.SampleRateHz != c.rate || info.FormatBitrate != c.formatBitBps {
			t.Errorf("%s: got %+v", c.name, info)
		}
	}

	// FLAC with no bits_per_raw_sample still reads its depth off the sample format.
	info, _ := ParseAudio([]byte(`{"streams":[{"codec_name":"flac","codec_type":"audio","sample_fmt":"s16","sample_rate":"44100"}],"format":{"bit_rate":"900000"}}`))
	if info.BitsPerSample != 16 || info.BitrateKbps != 900 {
		t.Errorf("fallbacks: %+v", info)
	}
	// No audio stream at all.
	if info, err := ParseAudio([]byte(`{"streams":[],"format":{"duration":"1"}}`)); err != nil || info.Codec != "" {
		t.Errorf("no audio: %+v %v", info, err)
	}
	if _, err := ParseAudio([]byte(`not json`)); err == nil {
		t.Error("garbage must be an error")
	}
	// Chapters come through, in seconds.
	info, _ = ParseAudio([]byte(`{"streams":[{"codec_name":"aac"}],"format":{},"chapters":[{"start_time":"0.0","end_time":"60.5","tags":{"title":"One"}}]}`))
	if len(info.Chapters) != 1 || info.Chapters[0].End != 60.5 || info.Chapters[0].Tags["title"] != "One" {
		t.Errorf("chapters: %+v", info.Chapters)
	}
}
