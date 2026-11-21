package mcpserver

import (
	"fmt"
	"math"

	"ir-studio/internal/irfile"
	"ir-studio/internal/irml"
	"ir-studio/internal/library"
)

type noiseCandidate struct {
	name       string
	reason     string
	confidence float32
}

func (s *Server) findNoiseRemotes(remotes []library.RemoteEntry, minConf float32) []noiseCandidate {
	var flagged []noiseCandidate
	for _, r := range remotes {
		loaded, err := s.store.LoadRemote(r)
		if err != nil || len(loaded.Signals) == 0 {
			continue
		}
		allNoise, minSigConf := s.evaluateRemoteNoise(loaded.Signals, minConf)
		if allNoise {
			flagged = append(flagged, noiseCandidate{
				name:       r.Name,
				reason:     "all signals classified as ambient noise",
				confidence: minSigConf,
			})
		}
	}
	return flagged
}

func (s *Server) evaluateRemoteNoise(signals []irfile.IRSignal, minConf float32) (bool, float32) {
	minSigConf := float32(1.0)
	for _, sig := range signals {
		pulses, carrierHz, err := s.signalToPulses(sig)
		if err != nil {
			return false, 0
		}
		cls := irml.ClassifySignal(pulses, carrierHz)
		noiseProb := cls.Probs[irml.ClassAmbientNoise]
		if !cls.IsNoise || noiseProb < minConf {
			return false, 0
		}
		if noiseProb < minSigConf {
			minSigConf = noiseProb
		}
	}
	return true, minSigConf
}

func (s *Server) resolvePulses(pulses []int32, remoteName, buttonName, pack string, carrierHz uint32) ([]int32, uint32, error) {
	if carrierHz == 0 {
		carrierHz = irfile.DefaultFreqHz
	}
	if len(pulses) > 0 {
		if err := irfile.ValidatePulseTrain(irfile.PulseTrain(pulses)); err != nil {
			return nil, 0, fmt.Errorf("invalid pulses: %w", err)
		}
		return pulses, carrierHz, nil
	}
	if remoteName == "" || buttonName == "" {
		return nil, 0, fmt.Errorf("provide either non-empty pulses or remote_name + button_name")
	}
	sig, err := s.lookupButton(remoteName, buttonName, pack)
	if err != nil {
		return nil, 0, err
	}
	pt, sigCarrier, err := s.signalToPulses(sig)
	if err != nil {
		return nil, 0, err
	}
	if sigCarrier > 0 && carrierHz == irfile.DefaultFreqHz {
		carrierHz = sigCarrier
	}
	return pt, carrierHz, nil
}

func (s *Server) signalToPulses(sig irfile.IRSignal) ([]int32, uint32, error) {
	carrierHz := sig.Frequency
	if carrierHz == 0 {
		carrierHz = irfile.DefaultFreqHz
	}
	if sig.Kind == irfile.SignalRaw {
		pt, err := irfile.FromRawData(sig.RawData)
		return []int32(pt), carrierHz, err
	}
	pt, err := s.registry.Encode(sig)
	return []int32(pt), carrierHz, err
}

func (s *Server) bestFingerprintMatch(pulses []int32, carrierHz uint32) map[string]any {
	entries, protoMap := s.collectFingerprints("")
	if len(entries) == 0 {
		return nil
	}
	ranked := irml.RankCandidates(irml.ExtractEmbedding(pulses, carrierHz), carrierHz, entries, nil)
	if len(ranked) == 0 || ranked[0].Similarity <= 0 {
		return nil
	}
	top := ranked[0]
	key := top.Pack + "|" + top.RemoteID + "|" + top.SignalName
	return map[string]any{
		"remote_name":    top.RemoteID,
		"pack":           top.Pack,
		"button_name":    top.SignalName,
		"similarity_pct": math.Round(float64(top.Similarity)*10000) / 100,
		"protocol":       protoMap[key],
	}
}

func (s *Server) collectFingerprints(packFilter string) ([]irml.FingerprintEntry, map[string]string) {
	var entries []irml.FingerprintEntry
	protoMap := make(map[string]string)
	appendRemote := func(r library.RemoteEntry, pack string) {
		loaded, err := s.store.LoadRemote(r)
		if err != nil {
			return
		}
		for _, sig := range loaded.Signals {
			pt, hz, err := s.signalToPulses(sig)
			if err != nil {
				continue
			}
			entries = append(entries, irml.FingerprintEntry{
				RemoteID:   r.Name,
				Pack:       pack,
				SignalName: sig.Name,
				CarrierHz:  hz,
				Embedding:  irml.ExtractEmbedding(pt, hz),
			})
			proto := sig.Protocol
			if proto == "" {
				proto = string(sig.Kind)
			}
			protoMap[pack+"|"+r.Name+"|"+sig.Name] = proto
		}
	}

	if packFilter == "" {
		if userRemotes, err := s.store.ListUserRemotes(); err == nil {
			for _, r := range userRemotes {
				appendRemote(r, "")
			}
		}
		if packs, err := s.store.ListDatabasePacks(); err == nil {
			for _, p := range packs {
				if packRemotes, err := s.store.ListPackRemotes(p.ID); err == nil {
					for _, r := range packRemotes {
						appendRemote(r, p.ID)
					}
				}
			}
		}
		return entries, protoMap
	}
	if packRemotes, err := s.store.ListPackRemotes(packFilter); err == nil {
		for _, r := range packRemotes {
			appendRemote(r, packFilter)
		}
	}
	return entries, protoMap
}
