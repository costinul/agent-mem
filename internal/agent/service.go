package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	models "agentmem/internal/models"
	"agentmem/internal/repository/agentrepo"

	"github.com/google/uuid"
)

type Service struct {
	repo agentrepo.Repository
}

func NewService(repo agentrepo.Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) CreateAgent(ctx context.Context, accountID, name string) (*models.Agent, error) {
	if !isUUID(accountID) {
		return nil, errors.New("account_id is invalid")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("name is required")
	}
	created, err := s.repo.CreateAgent(ctx, accountID, name)
	if err != nil {
		return nil, fmt.Errorf("create agent: %w", err)
	}
	return created, nil
}

func (s *Service) GetAgent(ctx context.Context, accountID, agentID string) (*models.Agent, error) {
	if !isUUID(accountID) {
		return nil, errors.New("account_id is invalid")
	}
	if !isUUID(agentID) {
		return nil, errors.New("agent_id is invalid")
	}
	found, err := s.repo.GetAgentByID(ctx, accountID, agentID)
	if err != nil {
		return nil, fmt.Errorf("get agent: %w", err)
	}
	if found == nil {
		return nil, errors.New("agent not found")
	}
	return found, nil
}

func (s *Service) DeleteAgent(ctx context.Context, accountID, agentID string) error {
	if !isUUID(accountID) {
		return errors.New("account_id is invalid")
	}
	if !isUUID(agentID) {
		return errors.New("agent_id is invalid")
	}
	deleted, err := s.repo.DeleteAgentByID(ctx, accountID, agentID)
	if err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}
	if !deleted {
		return errors.New("agent not found")
	}
	return nil
}

func (s *Service) CreateSession(ctx context.Context, accountID, agentID string) (*models.Session, error) {
	if !isUUID(accountID) {
		return nil, errors.New("account_id is invalid")
	}
	if !isUUID(agentID) {
		return nil, errors.New("agent_id is invalid")
	}
	if _, err := s.GetAgent(ctx, accountID, agentID); err != nil {
		return nil, err
	}
	session, err := s.repo.CreateSession(ctx, accountID, agentID)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return session, nil
}

func (s *Service) GetSession(ctx context.Context, accountID, agentID, sessionID string) (*models.Session, error) {
	if !isUUID(accountID) {
		return nil, errors.New("account_id is invalid")
	}
	if !isUUID(agentID) {
		return nil, errors.New("agent_id is invalid")
	}
	if !isUUID(sessionID) {
		return nil, errors.New("session_id is invalid")
	}
	found, err := s.repo.GetSessionByID(ctx, accountID, agentID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	if found == nil {
		return nil, errors.New("session not found")
	}
	return found, nil
}

func (s *Service) CloseSession(ctx context.Context, accountID, agentID, sessionID string) error {
	if !isUUID(accountID) {
		return errors.New("account_id is invalid")
	}
	if !isUUID(agentID) {
		return errors.New("agent_id is invalid")
	}
	if !isUUID(sessionID) {
		return errors.New("session_id is invalid")
	}
	closed, err := s.repo.CloseSessionByID(ctx, accountID, agentID, sessionID)
	if err != nil {
		return fmt.Errorf("close session: %w", err)
	}
	if !closed {
		existing, getErr := s.repo.GetSessionByID(ctx, accountID, agentID, sessionID)
		if getErr != nil {
			return fmt.Errorf("close session: %w", getErr)
		}
		if existing == nil {
			return errors.New("session not found")
		}
	}
	return nil
}

func isUUID(value string) bool {
	_, err := uuid.Parse(strings.TrimSpace(value))
	return err == nil
}
