package library

import "ir-studio/internal/irfile"

type tvbGoneSpec struct {
	brand string
	cat   TVBGoneCategory
	proto string
	addr  [4]byte
	cmd   [4]byte
}

func (s tvbGoneSpec) toSignal() irfile.IRSignal {
	freq := irfile.DefaultFreqHz
	switch s.proto {
	case "RC5", "RC6":
		freq = 36000
	case "SIRC":
		freq = 40000
	}
	return irfile.IRSignal{
		Name:      "Power",
		Kind:      irfile.SignalParsed,
		Protocol:  s.proto,
		Address:   s.addr,
		Command:   s.cmd,
		Frequency: freq,
		DutyCycle: irfile.DefaultDutyCycle,
	}
}

// tvbGoneSpecs holds the 100 embedded offline universal Power codes:
// 65 TVs, 20 Projectors, and 15 Soundbars/A/V receivers in priority order.
var tvbGoneSpecs = [TVBGoneCodeCount]tvbGoneSpec{
	// --- TVs (65 codes) ---
	{"Samsung", CategoryTV, "Samsung32", [4]byte{0x07}, [4]byte{0x02}},
	{"Samsung", CategoryTV, "Samsung32", [4]byte{0x07}, [4]byte{0x98}},
	{"Samsung", CategoryTV, "Samsung32", [4]byte{0x07}, [4]byte{0x99}},
	{"Samsung", CategoryTV, "Samsung32", [4]byte{0x0E}, [4]byte{0x02}},
	{"Samsung", CategoryTV, "Samsung32", [4]byte{0x08}, [4]byte{0x02}},
	{"LG", CategoryTV, "NEC", [4]byte{0x04}, [4]byte{0x08}},
	{"LG", CategoryTV, "NEC", [4]byte{0x04}, [4]byte{0xC5}},
	{"LG", CategoryTV, "NEC", [4]byte{0x04}, [4]byte{0xC4}},
	{"LG", CategoryTV, "NEC", [4]byte{0x20}, [4]byte{0x08}},
	{"LG", CategoryTV, "NEC", [4]byte{0x04}, [4]byte{0x09}},
	{"Sony", CategoryTV, "SIRC", [4]byte{0x01}, [4]byte{0x15}},
	{"Sony", CategoryTV, "SIRC", [4]byte{0x01}, [4]byte{0x2F}},
	{"Sony", CategoryTV, "SIRC", [4]byte{0x01}, [4]byte{0x2E}},
	{"Sony", CategoryTV, "SIRC", [4]byte{0x01, 0x01}, [4]byte{0x15}},
	{"Sony", CategoryTV, "SIRC", [4]byte{0xA4}, [4]byte{0x15}},
	{"Panasonic", CategoryTV, "Kaseikyo", [4]byte{0x02, 0x20, 0x08, 0x00}, [4]byte{0x00, 0x3D}},
	{"Panasonic", CategoryTV, "Kaseikyo", [4]byte{0x02, 0x20, 0x08, 0x00}, [4]byte{0x00, 0x3F}},
	{"Panasonic", CategoryTV, "Kaseikyo", [4]byte{0x02, 0x20, 0x08, 0x00}, [4]byte{0x00, 0x3E}},
	{"Panasonic", CategoryTV, "Kaseikyo", [4]byte{0x02, 0x20, 0x08, 0x04}, [4]byte{0x00, 0x3D}},
	{"Philips", CategoryTV, "RC5", [4]byte{0x00}, [4]byte{0x0C}},
	{"Philips", CategoryTV, "RC5", [4]byte{0x00}, [4]byte{0x3D}},
	{"Philips", CategoryTV, "RC6", [4]byte{0x00}, [4]byte{0x0C}},
	{"Philips", CategoryTV, "RC6", [4]byte{0x00}, [4]byte{0x3D}},
	{"Philips", CategoryTV, "RC6", [4]byte{0x04}, [4]byte{0x0C}},
	{"TCL", CategoryTV, "NEC", [4]byte{0x07}, [4]byte{0x02}},
	{"TCL", CategoryTV, "NECext", [4]byte{0xEA, 0xC7}, [4]byte{0x17, 0xE8}},
	{"TCL", CategoryTV, "NECext", [4]byte{0x00, 0xBF}, [4]byte{0x0D, 0xF2}},
	{"TCL", CategoryTV, "NEC", [4]byte{0xF0}, [4]byte{0x02}},
	{"Hisense", CategoryTV, "NEC", [4]byte{0x00}, [4]byte{0x0D}},
	{"Hisense", CategoryTV, "NEC", [4]byte{0x04}, [4]byte{0x0D}},
	{"Hisense", CategoryTV, "NEC", [4]byte{0x00}, [4]byte{0x12}},
	{"Hisense", CategoryTV, "NECext", [4]byte{0x04, 0xFB}, [4]byte{0x0D, 0xF2}},
	{"Toshiba", CategoryTV, "NEC", [4]byte{0x40}, [4]byte{0x12}},
	{"Toshiba", CategoryTV, "NEC", [4]byte{0x40}, [4]byte{0x1B}},
	{"Toshiba", CategoryTV, "NEC", [4]byte{0x45}, [4]byte{0x12}},
	{"Toshiba", CategoryTV, "NEC", [4]byte{0x40}, [4]byte{0x01}},
	{"Vizio", CategoryTV, "NEC", [4]byte{0x04}, [4]byte{0x0A}},
	{"Vizio", CategoryTV, "NEC", [4]byte{0x04}, [4]byte{0x0B}},
	{"Vizio", CategoryTV, "NEC", [4]byte{0x04}, [4]byte{0x0C}},
	{"Sharp", CategoryTV, "Kaseikyo", [4]byte{0xAA, 0x5A, 0x08, 0x0F}, [4]byte{0x01, 0x16}},
	{"Sharp", CategoryTV, "Kaseikyo", [4]byte{0xAA, 0x5A, 0x08, 0x0F}, [4]byte{0x01, 0x17}},
	{"Sharp", CategoryTV, "NEC", [4]byte{0x01}, [4]byte{0x16}},
	{"Sharp", CategoryTV, "NEC", [4]byte{0x10}, [4]byte{0x16}},
	{"Hitachi", CategoryTV, "NEC", [4]byte{0x50}, [4]byte{0x17}},
	{"Hitachi", CategoryTV, "NEC", [4]byte{0x50}, [4]byte{0x10}},
	{"Hitachi", CategoryTV, "NEC", [4]byte{0x51}, [4]byte{0x17}},
	{"Insignia", CategoryTV, "NEC", [4]byte{0x61}, [4]byte{0x0A}},
	{"Insignia", CategoryTV, "NEC", [4]byte{0x61}, [4]byte{0x08}},
	{"Roku", CategoryTV, "NECext", [4]byte{0xEA, 0xC7}, [4]byte{0x17, 0xE9}},
	{"Roku", CategoryTV, "NEC", [4]byte{0x57}, [4]byte{0x17}},
	{"Sanyo", CategoryTV, "NEC", [4]byte{0x38}, [4]byte{0x12}},
	{"Sanyo", CategoryTV, "NEC", [4]byte{0x38}, [4]byte{0x14}},
	{"JVC", CategoryTV, "NEC", [4]byte{0x03}, [4]byte{0x17}},
	{"JVC", CategoryTV, "NEC", [4]byte{0x03}, [4]byte{0x18}},
	{"Grundig", CategoryTV, "RC5", [4]byte{0x07}, [4]byte{0x0C}},
	{"Grundig", CategoryTV, "RC5", [4]byte{0x01}, [4]byte{0x0C}},
	{"Thomson", CategoryTV, "RC5", [4]byte{0x02}, [4]byte{0x0C}},
	{"Thomson", CategoryTV, "RC5", [4]byte{0x03}, [4]byte{0x0C}},
	{"Loewe", CategoryTV, "RC5", [4]byte{0x04}, [4]byte{0x0C}},
	{"Loewe", CategoryTV, "RC5", [4]byte{0x05}, [4]byte{0x0C}},
	{"Xiaomi", CategoryTV, "NEC", [4]byte{0x86}, [4]byte{0x00}},
	{"Xiaomi", CategoryTV, "NEC", [4]byte{0x86}, [4]byte{0x0F}},
	{"Skyworth", CategoryTV, "NEC", [4]byte{0x0E}, [4]byte{0x0C}},
	{"Skyworth", CategoryTV, "NEC", [4]byte{0x0E}, [4]byte{0x08}},
	{"Haier", CategoryTV, "NEC", [4]byte{0x08}, [4]byte{0x0B}},

	// --- Projectors (20 codes) ---
	{"Epson", CategoryProjector, "NEC", [4]byte{0x83}, [4]byte{0x90}},
	{"Epson", CategoryProjector, "NEC", [4]byte{0x83}, [4]byte{0x91}},
	{"Epson", CategoryProjector, "NEC", [4]byte{0x83}, [4]byte{0x92}},
	{"BenQ", CategoryProjector, "NEC", [4]byte{0x0C}, [4]byte{0x02}},
	{"BenQ", CategoryProjector, "NEC", [4]byte{0x0C}, [4]byte{0x0F}},
	{"BenQ", CategoryProjector, "NEC", [4]byte{0x0C}, [4]byte{0x03}},
	{"Optoma", CategoryProjector, "NEC", [4]byte{0x32}, [4]byte{0x02}},
	{"Optoma", CategoryProjector, "NEC", [4]byte{0x32}, [4]byte{0x2E}},
	{"Optoma", CategoryProjector, "NEC", [4]byte{0x32}, [4]byte{0x81}},
	{"ViewSonic", CategoryProjector, "NEC", [4]byte{0x5A}, [4]byte{0x02}},
	{"ViewSonic", CategoryProjector, "NEC", [4]byte{0x5A}, [4]byte{0x0F}},
	{"Acer", CategoryProjector, "NEC", [4]byte{0x13}, [4]byte{0x12}},
	{"Acer", CategoryProjector, "NEC", [4]byte{0x13}, [4]byte{0x14}},
	{"NEC_Display", CategoryProjector, "NEC", [4]byte{0x18}, [4]byte{0x08}},
	{"NEC_Display", CategoryProjector, "NEC", [4]byte{0x18}, [4]byte{0x14}},
	{"Panasonic", CategoryProjector, "Kaseikyo", [4]byte{0x02, 0x20, 0x09, 0x00}, [4]byte{0x00, 0x3D}},
	{"Panasonic", CategoryProjector, "Kaseikyo", [4]byte{0x02, 0x20, 0x09, 0x00}, [4]byte{0x00, 0x3F}},
	{"Sony", CategoryProjector, "SIRC", [4]byte{0x1A}, [4]byte{0x15}},
	{"Sony", CategoryProjector, "SIRC", [4]byte{0x1A}, [4]byte{0x2F}},
	{"Christie", CategoryProjector, "NEC", [4]byte{0x2A}, [4]byte{0x01}},

	// --- Soundbars & A/V Receivers (15 codes) ---
	{"Bose", CategorySoundbar, "NEC", [4]byte{0xBA}, [4]byte{0x4C}},
	{"Bose", CategorySoundbar, "NEC", [4]byte{0xBA}, [4]byte{0x4D}},
	{"Sonos", CategorySoundbar, "NEC", [4]byte{0x7A}, [4]byte{0x1E}},
	{"Sonos", CategorySoundbar, "NEC", [4]byte{0x7A}, [4]byte{0x1F}},
	{"Yamaha", CategorySoundbar, "NEC", [4]byte{0x7E}, [4]byte{0x2A}},
	{"Yamaha", CategorySoundbar, "NECext", [4]byte{0x7A, 0x85}, [4]byte{0x1D, 0xE2}},
	{"Denon", CategorySoundbar, "Kaseikyo", [4]byte{0x54, 0x32, 0x00, 0x02}, [4]byte{0x00, 0x01}},
	{"Denon", CategorySoundbar, "Kaseikyo", [4]byte{0x54, 0x32, 0x00, 0x02}, [4]byte{0x00, 0x02}},
	{"Onkyo", CategorySoundbar, "NECext", [4]byte{0xD2, 0x6D}, [4]byte{0x04, 0xFB}},
	{"Onkyo", CategorySoundbar, "NECext", [4]byte{0xD2, 0x6C}, [4]byte{0x04, 0xFB}},
	{"Pioneer", CategorySoundbar, "NEC", [4]byte{0xA5}, [4]byte{0x1C}},
	{"Pioneer", CategorySoundbar, "NEC", [4]byte{0xA5}, [4]byte{0x1D}},
	{"JBL", CategorySoundbar, "NEC", [4]byte{0x80}, [4]byte{0x01}},
	{"Polk", CategorySoundbar, "NEC", [4]byte{0x48}, [4]byte{0x12}},
	{"Klipsch", CategorySoundbar, "NEC", [4]byte{0x68}, [4]byte{0x0C}},
}
