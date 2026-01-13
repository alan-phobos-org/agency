package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

// SessionType distinguishes auth sessions from device sessions.
type SessionType string

const (
	SessionTypeAuth   SessionType = "auth"   // From password login (12h, sliding refresh)
	SessionTypeDevice SessionType = "device" // From device pairing (long-lived, revocable)
)

// Argon2id parameters
const (
	argonTime    = 1
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// Session durations
const (
	AuthSessionDuration = 12 * time.Hour
	PairingCodeTTL      = 10 * time.Minute
	SessionIDLength     = 32 // bytes (64 hex chars)
	PairingCodeLength   = 8  // characters
)

const base32Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

// AuthSession represents an authenticated session (login or device).
type AuthSession struct {
	ID        string      `json:"id"`
	Type      SessionType `json:"type"`
	Label     string      `json:"label,omitempty"` // Device name for device sessions
	CreatedAt time.Time   `json:"created_at"`
	LastSeen  time.Time   `json:"last_seen"`
	ExpiresAt time.Time   `json:"expires_at,omitempty"` // Zero for device sessions
	IPAddress string      `json:"ip_address"`
	UserAgent string      `json:"user_agent"`
}

// IsExpired checks if the session has expired.
func (s *AuthSession) IsExpired() bool {
	if s.Type == SessionTypeDevice {
		return false // Device sessions never expire
	}
	return time.Now().After(s.ExpiresAt)
}

// PairingCode represents a single-use pairing code.
type PairingCode struct {
	CodeHash  string    `json:"code_hash"`
	ExpiresAt time.Time `json:"expires_at"`
	Used      bool      `json:"used"`
}

// authStoreData is the JSON structure for persistence.
type authStoreData struct {
	Sessions     []*AuthSession `json:"sessions"`
	PairingCodes []*PairingCode `json:"pairing_codes"`
}

// AuthStore manages auth sessions and pairing codes.
type AuthStore struct {
	mu           sync.RWMutex
	sessions     map[string]*AuthSession
	pairingCodes []*PairingCode
	filePath     string
	passwordHash string // Argon2id encoded hash (memory only, not persisted)
}

// NewAuthStore creates a new auth store.
// If password is empty, authentication is disabled.
func NewAuthStore(filePath, password string) (*AuthStore, error) {
	s := &AuthStore{
		sessions:     make(map[string]*AuthSession),
		pairingCodes: make([]*PairingCode, 0),
		filePath:     filePath,
	}

	// Hash password if provided
	if password != "" {
		hash, err := hashPassword(password)
		if err != nil {
			return nil, fmt.Errorf("hashing password: %w", err)
		}
		s.passwordHash = hash
	}

	// Ensure directory exists
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating auth store directory: %w", err)
	}

	// Load existing sessions
	if err := s.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("loading auth store: %w", err)
	}

	return s, nil
}

// ValidatePassword checks if the provided password matches.
// Returns false if no password is configured.
func (s *AuthStore) ValidatePassword(password string) bool {
	if s.passwordHash == "" {
		return false
	}
	return verifyPassword(password, s.passwordHash)
}

// HasPassword returns true if a password is configured.
func (s *AuthStore) HasPassword() bool {
	return s.passwordHash != ""
}

// CreateAuthSession creates a new auth session from password login.
func (s *AuthStore) CreateAuthSession(ip, userAgent string) (*AuthSession, error) {
	id, err := generateSessionID()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	session := &AuthSession{
		ID:        id,
		Type:      SessionTypeAuth,
		CreatedAt: now,
		LastSeen:  now,
		ExpiresAt: now.Add(AuthSessionDuration),
		IPAddress: ip,
		UserAgent: userAgent,
	}

	s.mu.Lock()
	s.sessions[id] = session
	err = s.saveUnlocked()
	s.mu.Unlock()

	if err != nil {
		return nil, err
	}
	return session, nil
}

// CreateDeviceSession creates a new device session from a valid pairing code.
func (s *AuthStore) CreateDeviceSession(code, label, ip, userAgent string) (*AuthSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Find and validate the pairing code
	var validCode *PairingCode
	for _, pc := range s.pairingCodes {
		if pc.Used || time.Now().After(pc.ExpiresAt) {
			continue
		}
		if verifyPairingCode(code, pc.CodeHash) {
			validCode = pc
			break
		}
	}

	if validCode == nil {
		return nil, fmt.Errorf("invalid or expired pairing code")
	}

	// Mark code as used
	validCode.Used = true

	// Create device session
	id, err := generateSessionID()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	session := &AuthSession{
		ID:        id,
		Type:      SessionTypeDevice,
		Label:     label,
		CreatedAt: now,
		LastSeen:  now,
		// ExpiresAt is zero for device sessions (never expire)
		IPAddress: ip,
		UserAgent: userAgent,
	}

	s.sessions[id] = session
	if err := s.saveUnlocked(); err != nil {
		return nil, err
	}

	return session, nil
}

// GetSession retrieves a session by ID.
// Returns nil if not found or expired.
func (s *AuthStore) GetSession(id string) *AuthSession {
	s.mu.RLock()
	defer s.mu.RUnlock()

	session, ok := s.sessions[id]
	if !ok || session.IsExpired() {
		return nil
	}
	return session
}

// RefreshSession updates the last seen time and extends expiry for auth sessions.
// Returns false if session not found or expired.
func (s *AuthStore) RefreshSession(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[id]
	if !ok || session.IsExpired() {
		return false
	}

	now := time.Now()
	session.LastSeen = now
	if session.Type == SessionTypeAuth {
		session.ExpiresAt = now.Add(AuthSessionDuration)
	}

	// Save synchronously to ensure cleanup doesn't race with file writes
	s.saveUnlocked()

	return true
}

// DeleteSession removes a session.
func (s *AuthStore) DeleteSession(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, id)
	s.saveUnlocked()
}

// InvalidateAllSessions removes all sessions (for password change).
func (s *AuthStore) InvalidateAllSessions() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessions = make(map[string]*AuthSession)
	s.pairingCodes = make([]*PairingCode, 0)
	s.saveUnlocked()
}

// CreatePairingCode generates a new pairing code.
// Returns the plaintext code (only shown once).
func (s *AuthStore) CreatePairingCode() (string, error) {
	code, err := generatePairingCode()
	if err != nil {
		return "", err
	}

	hash, err := hashPairingCode(code)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Clean up expired/used codes
	s.pruneCodesUnlocked()

	s.pairingCodes = append(s.pairingCodes, &PairingCode{
		CodeHash:  hash,
		ExpiresAt: time.Now().Add(PairingCodeTTL),
		Used:      false,
	})

	if err := s.saveUnlocked(); err != nil {
		return "", err
	}

	return code, nil
}

// ListDeviceSessions returns all device sessions.
func (s *AuthStore) ListDeviceSessions() []*AuthSession {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var devices []*AuthSession
	for _, session := range s.sessions {
		if session.Type == SessionTypeDevice {
			devices = append(devices, session)
		}
	}
	return devices
}

// ListAllSessions returns all active sessions (for device management UI).
func (s *AuthStore) ListAllSessions() []*AuthSession {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var sessions []*AuthSession
	for _, session := range s.sessions {
		if !session.IsExpired() {
			sessions = append(sessions, session)
		}
	}
	return sessions
}

// pruneCodesUnlocked removes expired and used pairing codes.
// Must be called with lock held.
func (s *AuthStore) pruneCodesUnlocked() {
	now := time.Now()
	valid := make([]*PairingCode, 0, len(s.pairingCodes))
	for _, pc := range s.pairingCodes {
		if !pc.Used && now.Before(pc.ExpiresAt) {
			valid = append(valid, pc)
		}
	}
	s.pairingCodes = valid
}

// load reads sessions from disk.
func (s *AuthStore) load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}

	var stored authStoreData
	if err := json.Unmarshal(data, &stored); err != nil {
		return fmt.Errorf("parsing auth store: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Load sessions, filtering expired ones
	s.sessions = make(map[string]*AuthSession)
	for _, session := range stored.Sessions {
		if !session.IsExpired() {
			s.sessions[session.ID] = session
		}
	}

	// Load pairing codes, filtering expired/used ones
	now := time.Now()
	s.pairingCodes = make([]*PairingCode, 0)
	for _, pc := range stored.PairingCodes {
		if !pc.Used && now.Before(pc.ExpiresAt) {
			s.pairingCodes = append(s.pairingCodes, pc)
		}
	}

	return nil
}

// saveUnlocked persists sessions to disk.
// Must be called with lock held.
func (s *AuthStore) saveUnlocked() error {
	// Filter expired sessions before saving
	sessions := make([]*AuthSession, 0, len(s.sessions))
	for _, session := range s.sessions {
		if !session.IsExpired() {
			sessions = append(sessions, session)
		}
	}

	data := authStoreData{
		Sessions:     sessions,
		PairingCodes: s.pairingCodes,
	}

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling auth store: %w", err)
	}

	return os.WriteFile(s.filePath, jsonData, 0600) // Restrictive permissions
}

// generateSessionID creates a cryptographically random session ID.
func generateSessionID() (string, error) {
	bytes := make([]byte, SessionIDLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generating session ID: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

// generatePairingCode creates a random 8-character base32 code.
func generatePairingCode() (string, error) {
	bytes := make([]byte, PairingCodeLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generating pairing code: %w", err)
	}

	code := make([]byte, PairingCodeLength)
	for i := range code {
		code[i] = base32Alphabet[bytes[i]%32]
	}
	return string(code), nil
}

// hashPassword creates an Argon2id hash of the password.
func hashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}

	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	// Encode as: $argon2id$v=19$m=65536,t=1,p=4$<base64-salt>$<base64-hash>
	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64Salt, b64Hash), nil
}

// verifyPassword checks a password against an Argon2id hash.
func verifyPassword(password, encodedHash string) bool {
	// Parse the encoded hash
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}

	var version int
	var memory, time uint32
	var threads uint8
	_, err := fmt.Sscanf(parts[2], "v=%d", &version)
	if err != nil {
		return false
	}
	_, err = fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads)
	if err != nil {
		return false
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}

	expectedHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}

	// Compute hash with same parameters
	computedHash := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(expectedHash)))

	// Constant-time comparison
	return subtle.ConstantTimeCompare(computedHash, expectedHash) == 1
}

// hashPairingCode creates a hash for a pairing code (simpler than password).
func hashPairingCode(code string) (string, error) {
	salt := make([]byte, 8)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}

	// Use faster parameters for pairing codes (short-lived)
	hash := argon2.IDKey([]byte(code), salt, 1, 16*1024, 1, 16)

	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)

	return fmt.Sprintf("%s$%s", b64Salt, b64Hash), nil
}

// verifyPairingCode checks a pairing code against its hash.
func verifyPairingCode(code, encodedHash string) bool {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 2 {
		return false
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}

	expectedHash, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}

	computedHash := argon2.IDKey([]byte(code), salt, 1, 16*1024, 1, 16)
	return subtle.ConstantTimeCompare(computedHash, expectedHash) == 1
}
