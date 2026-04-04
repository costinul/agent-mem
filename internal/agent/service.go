package agent

import (
	"context"
	"fmt"
	"strings"

	"agentmem/internal/errs"
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
		return nil, errs.NewValidation("account_id is invalid")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errs.NewValidation("name is required")
	}
	created, err := s.repo.CreateAgent(ctx, accountID, name)
	if err != nil {
		return nil, fmt.Errorf("create agent: %w", err)
	}
	return created, nil
}

func (s *Service) GetAgent(ctx context.Context, accountID, agentID string) (*models.Agent, error) {
	if !isUUID(accountID) {
		return nil, errs.NewValidation("account_id is invalid")
	}
	if !isUUID(agentID) {
		return nil, errs.NewValidation("agent_id is invalid")
	}
	found, err := s.repo.GetAgentByID(ctx, accountID, agentID)
	if err != nil {
		return nil, fmt.Errorf("get agent: %w", err)
	}
	if found == nil {
		return nil, errs.NewNotFound("agent not found")
	}
	return found, nil
}

func (s *Service) DeleteAgent(ctx context.Context, accountID, agentID string) error {
	if !isUUID(accountID) {
		return errs.NewValidation("account_id is invalid")
	}
	if !isUUID(agentID) {
		return errs.NewValidation("agent_id is invalid")
	}
	deleted, err := s.repo.DeleteAgentByID(ctx, accountID, agentID)
	if err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}
	if !deleted {
		return errs.NewNotFound("agent not found")
	}
	return nil
}

func (s *Service) CreateThread(ctx context.Context, accountID, agentID string) (*models.Thread, error) {
	if !isUUID(accountID) {
		return nil, errs.NewValidation("account_id is invalid")
	}
	if !isUUID(agentID) {
		return nil, errs.NewValidation("agent_id is invalid")
	}
	if _, err := s.GetAgent(ctx, accountID, agentID); err != nil {
		return nil, err
	}
	thread, err := s.repo.CreateThread(ctx, accountID, agentID)
	if err != nil {
		return nil, fmt.Errorf("create thread: %w", err)
	}
	return thread, nil
}

func (s *Service) GetThread(ctx context.Context, accountID, threadID string) (*models.Thread, error) {
	if !isUUID(accountID) {
		return nil, errs.NewValidation("account_id is invalid")
	}
	if !isUUID(threadID) {
		return nil, errs.NewValidation("thread_id is invalid")
	}
	found, err := s.repo.GetThreadByID(ctx, accountID, threadID)
	if err != nil {
		return nil, fmt.Errorf("get thread: %w", err)
	}
	if found == nil {
		return nil, errs.NewNotFound("thread not found")
	}
	return found, nil
}

func (s *Service) DeleteThread(ctx context.Context, accountID, threadID string) error {
	if !isUUID(accountID) {
		return errs.NewValidation("account_id is invalid")
	}
	if !isUUID(threadID) {
		return errs.NewValidation("thread_id is invalid")
	}
	deleted, err := s.repo.DeleteThreadByID(ctx, accountID, threadID)
	if err != nil {
		return fmt.Errorf("delete thread: %w", err)
	}
	if !deleted {
		return errs.NewNotFound("thread not found")
	}
	return nil
}

func isUUID(value string) bool {
	_, err := uuid.Parse(strings.TrimSpace(value))
	return err == nil
}
