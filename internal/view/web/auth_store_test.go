package web

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func init() {
	// Use fast argon2 parameters for tests (1KB instead of 64MB/16KB)
	argonMemory = 1024
	argonPairingMem = 1024
}

func TestPasswordHashing(t *testing.T) {
	t.Parallel()

	hash, err := hashPassword("testpassword123")
	if err != nil {
		t.Fatalf("hashPassword failed: %v", err)
	}

	if hash == "" {
		t.Fatal("hash should not be empty")
	}

	// Verify format
	if hash[:9] != "$argon2id" {
		t.Errorf("hash should start with $argon2id, got: %s", hash[:20])
	}
}

func TestPasswordValidation(t *testing.T) {
	t.Parallel()

	hash, err := hashPassword("correctpassword")
	if err != nil {
		t.Fatalf("hashPassword failed: %v", err)
	}

	// Correct password
	if !verifyPassword("correctpassword", hash) {
		t.Error("verifyPassword should return true for correct password")
	}

	// Wrong password
	if verifyPassword("wrongpassword", hash) {
		t.Error("verifyPassword should return false for wrong password")
	}

	// Empty password
	if verifyPassword("", hash) {
		t.Error("verifyPassword should return false for empty password")
	}
}

func TestNewAuthStoreWithPassword(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	store, err := NewAuthStore(path, "mypassword")
	if err != nil {
		t.Fatalf("NewAuthStore failed: %v", err)
	}

	if !store.HasPassword() {
		t.Error("store should have password configured")
	}

	if !store.ValidatePassword("mypassword") {
		t.Error("ValidatePassword should return true for correct password")
	}

	if store.ValidatePassword("wrongpassword") {
		t.Error("ValidatePassword should return false for wrong password")
	}
}

func TestNewAuthStoreWithoutPassword(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	store, err := NewAuthStore(path, "")
	if err != nil {
		t.Fatalf("NewAuthStore failed: %v", err)
	}

	if store.HasPassword() {
		t.Error("store should not have password configured")
	}

	if store.ValidatePassword("anypassword") {
		t.Error("ValidatePassword should return false when no password configured")
	}
}

func TestCreateAuthSession(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewAuthStore(filepath.Join(dir, "auth.json"), "password")
	if err != nil {
		t.Fatalf("NewAuthStore failed: %v", err)
	}

	session, err := store.CreateAuthSession("192.168.1.1", "Mozilla/5.0")
	if err != nil {
		t.Fatalf("CreateAuthSession failed: %v", err)
	}

	if session.ID == "" {
		t.Error("session ID should not be empty")
	}
	if len(session.ID) != 64 { // 32 bytes * 2 hex chars
		t.Errorf("session ID should be 64 chars, got %d", len(session.ID))
	}
	if session.Type != SessionTypeAuth {
		t.Errorf("session type should be auth, got %s", session.Type)
	}
	if session.IPAddress != "192.168.1.1" {
		t.Errorf("IP address mismatch: got %s", session.IPAddress)
	}
	if session.ExpiresAt.IsZero() {
		t.Error("auth session should have expiry time")
	}
	if session.ExpiresAt.Before(time.Now().Add(11 * time.Hour)) {
		t.Error("auth session expiry should be ~12 hours from now")
	}
}

func TestGetSession(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewAuthStore(filepath.Join(dir, "auth.json"), "password")
	if err != nil {
		t.Fatalf("NewAuthStore failed: %v", err)
	}

	session, _ := store.CreateAuthSession("192.168.1.1", "Mozilla/5.0")

	// Get existing session
	found := store.GetSession(session.ID)
	if found == nil {
		t.Fatal("GetSession should return existing session")
	}
	if found.ID != session.ID {
		t.Error("session ID mismatch")
	}

	// Get non-existent session
	notFound := store.GetSession("nonexistent")
	if notFound != nil {
		t.Error("GetSession should return nil for non-existent session")
	}
}

func TestAuthSessionExpiry(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewAuthStore(filepath.Join(dir, "auth.json"), "password")
	if err != nil {
		t.Fatalf("NewAuthStore failed: %v", err)
	}

	session, _ := store.CreateAuthSession("192.168.1.1", "Mozilla/5.0")

	// Manually expire the session
	store.mu.Lock()
	session.ExpiresAt = time.Now().Add(-1 * time.Hour)
	store.mu.Unlock()

	// Should not find expired session
	found := store.GetSession(session.ID)
	if found != nil {
		t.Error("GetSession should return nil for expired session")
	}
}

func TestRefreshSession(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewAuthStore(filepath.Join(dir, "auth.json"), "password")
	if err != nil {
		t.Fatalf("NewAuthStore failed: %v", err)
	}

	session, _ := store.CreateAuthSession("192.168.1.1", "Mozilla/5.0")
	originalExpiry := session.ExpiresAt

	// Wait a tiny bit to ensure time difference
	time.Sleep(10 * time.Millisecond)

	// Refresh session
	if !store.RefreshSession(session.ID) {
		t.Error("RefreshSession should return true for valid session")
	}

	// Check expiry was extended
	store.mu.RLock()
	newExpiry := store.sessions[session.ID].ExpiresAt
	store.mu.RUnlock()

	if !newExpiry.After(originalExpiry) {
		t.Error("RefreshSession should extend expiry time")
	}

	// Refresh non-existent session
	if store.RefreshSession("nonexistent") {
		t.Error("RefreshSession should return false for non-existent session")
	}
}

func TestDeleteSession(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewAuthStore(filepath.Join(dir, "auth.json"), "password")
	if err != nil {
		t.Fatalf("NewAuthStore failed: %v", err)
	}

	session, _ := store.CreateAuthSession("192.168.1.1", "Mozilla/5.0")

	store.DeleteSession(session.ID)

	if store.GetSession(session.ID) != nil {
		t.Error("session should be deleted")
	}
}

func TestCreatePairingCode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewAuthStore(filepath.Join(dir, "auth.json"), "password")
	if err != nil {
		t.Fatalf("NewAuthStore failed: %v", err)
	}

	code, err := store.CreatePairingCode()
	if err != nil {
		t.Fatalf("CreatePairingCode failed: %v", err)
	}

	if len(code) != PairingCodeLength {
		t.Errorf("pairing code should be %d chars, got %d", PairingCodeLength, len(code))
	}

	// Verify code is base32
	for _, c := range code {
		found := false
		for _, valid := range base32Alphabet {
			if c == valid {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("pairing code contains invalid char: %c", c)
		}
	}
}

func TestCreateDeviceSession(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewAuthStore(filepath.Join(dir, "auth.json"), "password")
	if err != nil {
		t.Fatalf("NewAuthStore failed: %v", err)
	}

	// Create pairing code
	code, err := store.CreatePairingCode()
	if err != nil {
		t.Fatalf("CreatePairingCode failed: %v", err)
	}

	// Create device session with valid code
	session, err := store.CreateDeviceSession(code, "iPhone", "192.168.1.2", "Safari")
	if err != nil {
		t.Fatalf("CreateDeviceSession failed: %v", err)
	}

	if session.Type != SessionTypeDevice {
		t.Errorf("session type should be device, got %s", session.Type)
	}
	if session.Label != "iPhone" {
		t.Errorf("label mismatch: got %s", session.Label)
	}
	if !session.ExpiresAt.IsZero() {
		t.Error("device session should not have expiry time")
	}
}

func TestPairingCodeSingleUse(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewAuthStore(filepath.Join(dir, "auth.json"), "password")
	if err != nil {
		t.Fatalf("NewAuthStore failed: %v", err)
	}

	code, _ := store.CreatePairingCode()

	// First use should succeed
	_, err = store.CreateDeviceSession(code, "Device1", "192.168.1.1", "UA")
	if err != nil {
		t.Fatalf("first use should succeed: %v", err)
	}

	// Second use should fail
	_, err = store.CreateDeviceSession(code, "Device2", "192.168.1.2", "UA")
	if err == nil {
		t.Error("second use of pairing code should fail")
	}
}

func TestPairingCodeInvalid(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewAuthStore(filepath.Join(dir, "auth.json"), "password")
	if err != nil {
		t.Fatalf("NewAuthStore failed: %v", err)
	}

	// Try with invalid code
	_, err = store.CreateDeviceSession("INVALID1", "Device", "192.168.1.1", "UA")
	if err == nil {
		t.Error("invalid pairing code should fail")
	}
}

func TestDeviceSessionNeverExpires(t *testing.T) {
	t.Parallel()

	session := &AuthSession{
		ID:        "test-id",
		Type:      SessionTypeDevice,
		ExpiresAt: time.Time{}, // Zero time
	}

	if session.IsExpired() {
		t.Error("device session should never expire")
	}
}

func TestInvalidateAllSessions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewAuthStore(filepath.Join(dir, "auth.json"), "password")
	if err != nil {
		t.Fatalf("NewAuthStore failed: %v", err)
	}

	// Create some sessions
	s1, _ := store.CreateAuthSession("192.168.1.1", "UA1")
	code, _ := store.CreatePairingCode()
	s2, _ := store.CreateDeviceSession(code, "Device", "192.168.1.2", "UA2")

	store.InvalidateAllSessions()

	if store.GetSession(s1.ID) != nil {
		t.Error("auth session should be invalidated")
	}
	if store.GetSession(s2.ID) != nil {
		t.Error("device session should be invalidated")
	}
}

func TestListDeviceSessions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewAuthStore(filepath.Join(dir, "auth.json"), "password")
	if err != nil {
		t.Fatalf("NewAuthStore failed: %v", err)
	}

	// Create auth session (should not appear in list)
	store.CreateAuthSession("192.168.1.1", "UA1")

	// Create device sessions
	code1, _ := store.CreatePairingCode()
	store.CreateDeviceSession(code1, "Device1", "192.168.1.2", "UA2")

	code2, _ := store.CreatePairingCode()
	store.CreateDeviceSession(code2, "Device2", "192.168.1.3", "UA3")

	devices := store.ListDeviceSessions()
	if len(devices) != 2 {
		t.Errorf("expected 2 device sessions, got %d", len(devices))
	}
}

func TestStorePersistence(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	// Create store and add session
	store1, err := NewAuthStore(path, "password")
	if err != nil {
		t.Fatalf("NewAuthStore failed: %v", err)
	}

	session, _ := store1.CreateAuthSession("192.168.1.1", "Mozilla/5.0")

	// Create new store from same file
	store2, err := NewAuthStore(path, "password")
	if err != nil {
		t.Fatalf("NewAuthStore (reload) failed: %v", err)
	}

	// Session should be loaded
	found := store2.GetSession(session.ID)
	if found == nil {
		t.Fatal("session should be persisted and reloaded")
	}
	if found.IPAddress != "192.168.1.1" {
		t.Errorf("session data mismatch: got %s", found.IPAddress)
	}
}

func TestStoreFilePermissions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	store, err := NewAuthStore(path, "password")
	if err != nil {
		t.Fatalf("NewAuthStore failed: %v", err)
	}

	// Create a session to trigger file write
	store.CreateAuthSession("192.168.1.1", "UA")

	// Check file permissions
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}

	// Should be 0600 (owner read/write only)
	if info.Mode().Perm() != 0600 {
		t.Errorf("file permissions should be 0600, got %o", info.Mode().Perm())
	}
}

func TestGenerateSessionID(t *testing.T) {
	t.Parallel()

	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id, err := generateSessionID()
		if err != nil {
			t.Fatalf("generateSessionID failed: %v", err)
		}
		if len(id) != 64 {
			t.Errorf("session ID should be 64 chars, got %d", len(id))
		}
		if ids[id] {
			t.Error("generateSessionID produced duplicate ID")
		}
		ids[id] = true
	}
}

func TestGeneratePairingCode(t *testing.T) {
	t.Parallel()

	codes := make(map[string]bool)
	for i := 0; i < 100; i++ {
		code, err := generatePairingCode()
		if err != nil {
			t.Fatalf("generatePairingCode failed: %v", err)
		}
		if len(code) != PairingCodeLength {
			t.Errorf("pairing code should be %d chars, got %d", PairingCodeLength, len(code))
		}
		if codes[code] {
			t.Error("generatePairingCode produced duplicate code")
		}
		codes[code] = true
	}
}

func TestPairingCodeHashing(t *testing.T) {
	t.Parallel()

	code := "TESTCODE"

	hash, err := hashPairingCode(code)
	if err != nil {
		t.Fatalf("hashPairingCode failed: %v", err)
	}

	if !verifyPairingCode(code, hash) {
		t.Error("verifyPairingCode should return true for correct code")
	}

	if verifyPairingCode("WRONGCODE", hash) {
		t.Error("verifyPairingCode should return false for wrong code")
	}
}
