package web

import (
	"sync"
	"time"
)

// SessionTask represents a task within a session
type SessionTask struct {
	TaskID string `json:"task_id"`
	State  string `json:"state"`
	Prompt string `json:"prompt"`
}

// Session represents a conversation session
type Session struct {
	ID        string        `json:"id"`
	AgentURL  string        `json:"agent_url"`
	Tasks     []SessionTask `json:"tasks"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// SessionStore provides thread-safe storage for sessions
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewSessionStore creates a new session store
func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*Session),
	}
}

// Get retrieves a session by ID
func (s *SessionStore) Get(id string) (*Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[id]
	return session, ok
}

// GetAll returns all sessions
func (s *SessionStore) GetAll() []*Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		result = append(result, session)
	}
	return result
}

// AddTask adds a task to a session, creating the session if it doesn't exist
func (s *SessionStore) AddTask(sessionID, agentURL, taskID, state, prompt string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	session, ok := s.sessions[sessionID]
	if !ok {
		session = &Session{
			ID:        sessionID,
			AgentURL:  agentURL,
			Tasks:     []SessionTask{},
			CreatedAt: now,
		}
		s.sessions[sessionID] = session
	}

	session.Tasks = append(session.Tasks, SessionTask{
		TaskID: taskID,
		State:  state,
		Prompt: prompt,
	})
	session.UpdatedAt = now
}

// UpdateTaskState updates the state of a specific task in a session
func (s *SessionStore) UpdateTaskState(sessionID, taskID, state string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return false
	}

	for i := range session.Tasks {
		if session.Tasks[i].TaskID == taskID {
			session.Tasks[i].State = state
			session.UpdatedAt = time.Now()
			return true
		}
	}
	return false
}

// Delete removes a session
func (s *SessionStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}
