package library

import (
	"slices"

	"ir-studio/internal/irfile"
)

const (
	// VirtualPackTVService is the built-in read-only discrete & service TV virtual pack.
	VirtualPackTVService = "GEN:TV-Service"
)

type tvServiceBtn struct {
	name string
	cmd  byte
}

type tvServiceSpec struct {
	name     string
	protocol string
	freqHz   uint32
	addr     [4]byte
	buttons  []tvServiceBtn
}

var tvServiceCatalog = []tvServiceSpec{
	{
		name:     "LG_Service_Discrete",
		protocol: "NEC",
		freqHz:   38000,
		addr:     [4]byte{0x04, 0x00, 0x00, 0x00},
		buttons: []tvServiceBtn{
			{name: "Power_ON", cmd: 0xC4},
			{name: "Power_OFF", cmd: 0xC5},
			{name: "HDMI_1", cmd: 0xCE},
			{name: "HDMI_2", cmd: 0xCC},
			{name: "HDMI_3", cmd: 0xE9},
			{name: "HDMI_4", cmd: 0xDA},
			{name: "IN_START", cmd: 0xFB},
			{name: "EZ_ADJUST", cmd: 0xFF},
			{name: "IN_STOP", cmd: 0xFA},
		},
	},
	{
		name:     "Samsung_Service_Discrete",
		protocol: "Samsung32",
		freqHz:   38000,
		addr:     [4]byte{0x07, 0x00, 0x00, 0x00},
		buttons: []tvServiceBtn{
			{name: "Power_ON", cmd: 0x99},
			{name: "Power_OFF", cmd: 0x98},
			{name: "HDMI_1", cmd: 0xE9},
			{name: "HDMI_2", cmd: 0xBE},
			{name: "HDMI_3", cmd: 0xC2},
			{name: "HDMI_4", cmd: 0xC5},
			{name: "Service_Info", cmd: 0x1F},
			{name: "Service_Menu", cmd: 0x1A},
			{name: "Service_Mute", cmd: 0x0F},
			{name: "Service_Factory", cmd: 0x3B},
		},
	},
	{
		name:     "Sony_Discrete",
		protocol: "SIRC",
		freqHz:   40000,
		addr:     [4]byte{0x01, 0x00, 0x00, 0x00},
		buttons: []tvServiceBtn{
			{name: "Power_ON", cmd: 0x2E},
			{name: "Power_OFF", cmd: 0x2F},
			{name: "HDMI_1", cmd: 0x25},
			{name: "HDMI_2", cmd: 0x26},
			{name: "HDMI_3", cmd: 0x27},
			{name: "HDMI_4", cmd: 0x28},
			{name: "Display_Info", cmd: 0x3A},
		},
	},
	{
		name:     "Panasonic_Discrete",
		protocol: "Kaseikyo",
		freqHz:   37000,
		addr:     [4]byte{0x02, 0x20, 0x00, 0x00},
		buttons: []tvServiceBtn{
			{name: "Power_ON", cmd: 0x3E},
			{name: "Power_OFF", cmd: 0x3F},
			{name: "HDMI_1", cmd: 0xB0},
			{name: "HDMI_2", cmd: 0xB1},
			{name: "HDMI_3", cmd: 0xB2},
			{name: "HDMI_4", cmd: 0xB3},
		},
	},
	{
		name:     "Philips_Discrete",
		protocol: "RC5",
		freqHz:   36000,
		addr:     [4]byte{0x00, 0x00, 0x00, 0x00},
		buttons: []tvServiceBtn{
			{name: "Power_ON", cmd: 0x3F},
			{name: "Power_OFF", cmd: 0x3D},
			{name: "HDMI_1", cmd: 0x38},
			{name: "HDMI_2", cmd: 0x39},
			{name: "HDMI_3", cmd: 0x3A},
			{name: "HDMI_4", cmd: 0x3B},
		},
	},
}

// TVServiceRemoteCount returns the number of compiled-in remotes in GEN:TV-Service.
func TVServiceRemoteCount() int {
	return len(tvServiceCatalog)
}

// TVServiceRemotes returns read-only RemoteEntry definitions for GEN:TV-Service
// with zero disk I/O.
func TVServiceRemotes() []RemoteEntry {
	out := make([]RemoteEntry, len(tvServiceCatalog))
	for i, sp := range tvServiceCatalog {
		sigs := buildTVServiceSignals(sp)
		rel := sp.name + ".ir"
		out[i] = RemoteEntry{
			Name:        sp.name,
			RelPath:     rel,
			FullPath:    VirtualPackTVService + "/" + rel,
			SourcePack:  VirtualPackTVService,
			ReadOnly:    true,
			ButtonCount: len(sigs),
			Protocols:   []string{sp.protocol},
			Signals:     sigs,
		}
	}
	return out
}

func buildTVServiceSignals(sp tvServiceSpec) []irfile.IRSignal {
	sigs := make([]irfile.IRSignal, len(sp.buttons))
	for i, btn := range sp.buttons {
		sigs[i] = irfile.IRSignal{
			Name:      btn.name,
			Kind:      irfile.SignalParsed,
			Protocol:  sp.protocol,
			Address:   sp.addr,
			Command:   [4]byte{btn.cmd, 0x00, 0x00, 0x00},
			Frequency: sp.freqHz,
			DutyCycle: irfile.DefaultDutyCycle,
		}
	}
	return sigs
}

func virtualPackRemotes(pack string) []RemoteEntry {
	switch pack {
	case VirtualPackTVBGone:
		return TVBGoneRemotes()
	case VirtualPackTVService:
		return TVServiceRemotes()
	default:
		return nil
	}
}

func cloneVirtualRemote(entry RemoteEntry) RemoteEntry {
	cp := entry
	cp.ReadOnly = true
	cp.ButtonCount = len(cp.Signals)
	cp.Protocols = collectProtocols(cp.Signals)
	cp.Signals = slices.Clone(entry.Signals)
	return cp
}
