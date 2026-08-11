package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	artifactsDir        = "artifacts"
	artifactMetaFile    = "meta.json"
	artifactPayloadFile = "payload"
	maxArtifactBytes    = 20 << 20
)

func (s Store) PublishArtifact(deliveryID string, meta ArtifactMeta, payload []byte) (ArtifactMeta, error) {
	if len(payload) > maxArtifactBytes {
		return ArtifactMeta{}, fmt.Errorf("artifact exceeds %d bytes", maxArtifactBytes)
	}
	release, err := s.AcquireDeliveryLock(deliveryID)
	if err != nil {
		return ArtifactMeta{}, err
	}
	defer release()
	if _, err := s.loadDeliveryUnlocked(deliveryID); err != nil {
		return ArtifactMeta{}, err
	}
	if strings.TrimSpace(meta.Kind) == "" || strings.TrimSpace(meta.Producer) == "" {
		return ArtifactMeta{}, errors.New("artifact kind and producer are required")
	}
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])
	meta.SchemaVersion = SchemaVersion
	meta.ID = "sha256:" + hash
	meta.DeliveryID = deliveryID
	meta.ContentHash = meta.ID
	meta.Size = int64(len(payload))
	meta.CreatedAt = s.now()
	dir := filepath.Join(s.deliveryDir(deliveryID), artifactsDir, hash)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ArtifactMeta{}, err
	}
	payloadPath := filepath.Join(dir, artifactPayloadFile)
	if existing, readErr := os.ReadFile(payloadPath); readErr == nil {
		existingHash := sha256.Sum256(existing)
		if existingHash != sum {
			return ArtifactMeta{}, errors.New("artifact hash collision or corrupted payload")
		}
		var stored ArtifactMeta
		if err := readJSON(filepath.Join(dir, artifactMetaFile), &stored); err != nil {
			return ArtifactMeta{}, err
		}
		return stored, nil
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return ArtifactMeta{}, readErr
	}
	if err := atomicWriteBytes(payloadPath, payload, 0644); err != nil {
		return ArtifactMeta{}, err
	}
	if err := s.writeJSON(filepath.Join(dir, artifactMetaFile), meta); err != nil {
		return ArtifactMeta{}, err
	}
	if err := s.appendEventUnlocked(deliveryID, Event{
		SchemaVersion: SchemaVersion,
		ID:            newEventID(meta.CreatedAt),
		DeliveryID:    deliveryID,
		NodeID:        meta.NodeID,
		Type:          "DeliveryArtifactPublished",
		Time:          meta.CreatedAt,
		Data: map[string]any{
			"artifact_id": meta.ID,
			"kind":        meta.Kind,
			"producer":    meta.Producer,
			"size":        meta.Size,
		},
	}); err != nil {
		return ArtifactMeta{}, err
	}
	return meta, nil
}

func (s Store) ReadArtifact(deliveryID string, artifactID string) (ArtifactMeta, []byte, error) {
	hash, err := artifactHash(artifactID)
	if err != nil {
		return ArtifactMeta{}, nil, err
	}
	dir := filepath.Join(s.deliveryDir(deliveryID), artifactsDir, hash)
	var meta ArtifactMeta
	if err := readJSON(filepath.Join(dir, artifactMetaFile), &meta); err != nil {
		return ArtifactMeta{}, nil, err
	}
	payload, err := os.ReadFile(filepath.Join(dir, artifactPayloadFile))
	if err != nil {
		return ArtifactMeta{}, nil, err
	}
	sum := sha256.Sum256(payload)
	if hex.EncodeToString(sum[:]) != hash || meta.ContentHash != "sha256:"+hash {
		return ArtifactMeta{}, nil, errors.New("artifact content hash mismatch")
	}
	return meta, payload, nil
}

func artifactHash(id string) (string, error) {
	id = strings.TrimSpace(id)
	hash := strings.TrimPrefix(id, "sha256:")
	if len(hash) != 64 {
		return "", errors.New("invalid artifact id")
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return "", errors.New("invalid artifact id")
	}
	return hash, nil
}

func atomicWriteBytes(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	removeTemp = false
	return nil
}
