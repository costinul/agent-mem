package memengine

import (
	"github.com/costinul/bwai/bwaiclient"
)

type MemoryEngine struct {
	client *bwaiclient.BWAIClient
}

func NewMemoryEngine(client *bwaiclient.BWAIClient) *MemoryEngine {
	return &MemoryEngine{
		client: client,
	}
}
