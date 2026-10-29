package admin

import "github.com/Wei-Shaw/sub2api/internal/service"

type ChannelQualityHandler struct {
	operations service.QualityOperationsRepository
}

func NewChannelQualityHandler(operations service.QualityOperationsRepository) *ChannelQualityHandler {
	return &ChannelQualityHandler{operations: operations}
}
