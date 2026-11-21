package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"ir-studio/internal/irfile"
	"ir-studio/internal/irml"
	"ir-studio/internal/library"
)

const (
	defaultTopK           = 5
	maxTopK               = 10
	defaultMinNoiseConf   = 0.75
	minNoiseConfThreshold = 0.50
	maxNoiseConfThreshold = 0.99
)

func (s *Server) toolAnalyzePulses(_ context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		Pulses     []int32 `json:"pulses"`
		RemoteName string  `json:"remote_name"`
		ButtonName string  `json:"button_name"`
		Pack       string  `json:"pack"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	pulses, _, err := s.resolvePulses(args.Pulses, args.RemoteName, args.ButtonName, args.Pack, 0)
	if err != nil {
		return errResult(err.Error()), nil
	}
	dst := make([]int32, 0, len(pulses))
	denoised, clusters := irml.DenoisePulses(dst, pulses, irml.DefaultGlitchMaxUs)

	out := map[string]any{
		"clusters": map[string]any{
			"short_us":         clusters.ShortUs,
			"long_us":          clusters.LongUs,
			"sync_us":          clusters.HeaderUs,
			"jitter_ratio":     clusters.JitterRatio,
			"glitches_removed": clusters.GlitchesRemoved,
			"encoding_hint":    clusters.EncodingHint,
			"valid":            clusters.Valid,
		},
		"denoised_pulses": denoised,
	}
	if sig, ok := s.registry.Decode(irfile.PulseTrain(denoised)); ok {
		out["decoded_protocol"] = signalToDTO(sig)
	}
	return okJSON(out), nil
}

func (s *Server) toolClassifySignal(_ context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		Pulses     []int32 `json:"pulses"`
		RemoteName string  `json:"remote_name"`
		ButtonName string  `json:"button_name"`
		Pack       string  `json:"pack"`
		CarrierHz  uint32  `json:"carrier_hz"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	pulses, carrierHz, err := s.resolvePulses(args.Pulses, args.RemoteName, args.ButtonName, "", args.CarrierHz)
	if err != nil && args.Pack != "" && len(args.Pulses) == 0 {
		pulses, carrierHz, err = s.resolvePulses(nil, args.RemoteName, args.ButtonName, args.Pack, args.CarrierHz)
	}
	if err != nil {
		return errResult(err.Error()), nil
	}
	feat := irml.ExtractFeatures(pulses, carrierHz)
	cls := irml.ClassifyFeatures(feat)
	out := classificationDTO(cls)
	out["features"] = feat
	return okJSON(out), nil
}

func classificationDTO(cls irml.Classification) map[string]any {
	return map[string]any{
		"label":      cls.Class.String(),
		"confidence": cls.Confidence,
		"is_noise":   cls.IsNoise,
		"probabilities": map[string]float32{
			irml.ClassTVRemote.String():     cls.Probs[irml.ClassTVRemote],
			irml.ClassACStateFrame.String(): cls.Probs[irml.ClassACStateFrame],
			irml.ClassAmbientNoise.String(): cls.Probs[irml.ClassAmbientNoise],
		},
	}
}

func (s *Server) toolMatchFingerprint(_ context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		Pulses     []int32 `json:"pulses"`
		RemoteName string  `json:"remote_name"`
		ButtonName string  `json:"button_name"`
		CarrierHz  uint32  `json:"carrier_hz"`
		TopK       int     `json:"top_k"`
		Pack       string  `json:"pack"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	topK := args.TopK
	if topK == 0 {
		topK = defaultTopK
	}
	if topK < 1 || topK > maxTopK {
		return errResult(fmt.Sprintf("top_k must be between 1 and %d", maxTopK)), nil
	}
	if args.Pack != "" {
		if err := ValidateSafeName(args.Pack); err != nil {
			return errResult(err.Error()), nil
		}
	}
	pulses, carrierHz, err := s.resolvePulses(args.Pulses, args.RemoteName, args.ButtonName, "", args.CarrierHz)
	if err != nil && args.Pack != "" && len(args.Pulses) == 0 {
		pulses, carrierHz, err = s.resolvePulses(nil, args.RemoteName, args.ButtonName, args.Pack, args.CarrierHz)
	}
	if err != nil {
		return errResult(err.Error()), nil
	}

	query := irml.ExtractEmbedding(pulses, carrierHz)
	entries, protoMap := s.collectFingerprints(args.Pack)
	ranked := irml.RankCandidates(query, carrierHz, entries, nil)
	if len(ranked) > topK {
		ranked = ranked[:topK]
	}

	matches := make([]map[string]any, 0, len(ranked))
	for _, m := range ranked {
		key := m.Pack + "|" + m.RemoteID + "|" + m.SignalName
		matches = append(matches, map[string]any{
			"remote_name":    m.RemoteID,
			"pack":           m.Pack,
			"button_name":    m.SignalName,
			"similarity_pct": math.Round(float64(m.Similarity)*10000) / 100,
			"protocol":       protoMap[key],
		})
	}
	return okJSON(map[string]any{
		"embedding": query,
		"matches":   matches,
	}), nil
}

func (s *Server) toolRankProbePack(_ context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		Pack            string  `json:"pack"`
		ActionFilter    string  `json:"action_filter"`
		ReferencePulses []int32 `json:"reference_pulses"`
		ReferenceRemote string  `json:"reference_remote"`
		ReferenceButton string  `json:"reference_button"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	if err := ValidateSafeName(args.Pack); err != nil {
		return errResult(err.Error()), nil
	}
	pulses, carrierHz, err := s.resolvePulses(args.ReferencePulses, args.ReferenceRemote, args.ReferenceButton, "", 0)
	if err != nil {
		return errResult(err.Error()), nil
	}
	action := library.ProbeAction(strings.TrimSpace(args.ActionFilter))
	cands, _, err := library.FilterPackCandidates(s.store, args.Pack, action)
	if err != nil {
		return errResult("filter pack candidates: " + err.Error()), nil
	}
	sess := library.NewProbeSession(cands, library.MinProbeInterval)
	sess.RankByEmbedding(irml.ExtractEmbedding(pulses, carrierHz), carrierHz, s.registry)

	ranked := sess.Candidates()
	out := make([]map[string]any, 0, len(ranked))
	for i, c := range ranked {
		out = append(out, map[string]any{
			"remote_name":    c.Remote.Name,
			"button_name":    c.Signal.Name,
			"similarity_pct": math.Round(float64(c.Score)*10000) / 100,
			"rank":           i + 1,
		})
	}
	return okJSON(map[string]any{
		"pack":              args.Pack,
		"action_filter":     args.ActionFilter,
		"ranked_candidates": out,
	}), nil
}

func (s *Server) toolPurgeNoise(_ context.Context, raw json.RawMessage) (toolResult, *rpcError) {
	var args struct {
		DryRun        *bool    `json:"dry_run"`
		Confirm       bool     `json:"confirm"`
		MinConfidence *float64 `json:"min_confidence"`
	}
	if rpcErr := decodeArgs(raw, &args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	dryRun := true
	if args.DryRun != nil {
		dryRun = *args.DryRun
	}
	if !dryRun && !args.Confirm {
		return errResult("confirm: true is required when dry_run is false"), nil
	}
	minConf := defaultMinNoiseConf
	if args.MinConfidence != nil {
		minConf = *args.MinConfidence
	}
	if minConf < minNoiseConfThreshold || minConf > maxNoiseConfThreshold {
		return errResult("min_confidence must be between 0.50 and 0.99"), nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	remotes, err := s.store.ListUserRemotes()
	if err != nil {
		return errResult("list user remotes: " + err.Error()), nil
	}
	flagged := s.findNoiseRemotes(remotes, float32(minConf))
	deletedCount := 0
	if !dryRun {
		for _, f := range flagged {
			if err := s.store.DeleteUserRemote(f.name); err == nil {
				deletedCount++
			}
		}
	}

	flaggedDTO := make([]map[string]any, 0, len(flagged))
	for _, f := range flagged {
		flaggedDTO = append(flaggedDTO, map[string]any{
			"name":             f.name,
			"reason":           f.reason,
			"noise_confidence": f.confidence,
		})
	}
	return okJSON(map[string]any{
		"dry_run":         dryRun,
		"scanned_count":   len(remotes),
		"flagged_remotes": flaggedDTO,
		"deleted_count":   deletedCount,
	}), nil
}
