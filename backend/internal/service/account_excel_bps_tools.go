package service

const ExcelBPSOmitUnsupportedToolsKey = "openai_excel_bps_omit_unsupported_tools"

// IsExcelBPSOmitUnsupportedToolsEnabled opts into the bridge's hosted-tool
// omission and capability notice. Forced selections still fail validation.
func (a *Account) IsExcelBPSOmitUnsupportedToolsEnabled() bool {
	return a.IsExcelBPSEnabled() && a.Extra[ExcelBPSOmitUnsupportedToolsKey] == true
}
