package service

import "context"

const SettingKeyExcelBPSEnabled = "excel_bps_enabled"

const ExcelBPSAuthorizationPendingKey = "openai_excel_bps_authorization_pending"

// Read the live setting so disabling BPS applies to every gateway instance.
// The unset value preserves existing installations' account-level choices.
func (s *SettingService) GetProtocolFeatureEnabled(ctx context.Context, key string) (bool, error) {
	if s == nil || s.settingRepo == nil {
		return key == SettingKeyExcelBPSEnabled, nil
	}
	values, err := s.settingRepo.GetMultiple(ctx, []string{key})
	if err != nil {
		return false, err
	}
	value := values[key]
	if key == SettingKeyExcelBPSEnabled && value == "" {
		return true, nil
	}
	return value == "true", nil
}

func (s *OpenAIGatewayService) excelBPSGloballyEnabled(ctx context.Context) bool {
	if s == nil || s.settingService == nil {
		return true
	}
	enabled, err := s.settingService.GetProtocolFeatureEnabled(ctx, SettingKeyExcelBPSEnabled)
	return err == nil && enabled
}
