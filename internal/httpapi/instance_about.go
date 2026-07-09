package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/instancesettings"
)

// instanceAboutResponse is the public "about this instance" long-form document
// (spec instance-platform-info): the operator-authored markdown texts behind
// the /about page. Every field is RAW markdown ("" when unset); rendering and
// sanitisation are the client's job.
type instanceAboutResponse struct {
	Description         string `json:"description"`
	Terms               string `json:"terms"`
	CodeOfConduct       string `json:"code_of_conduct"`
	ModerationInfo      string `json:"moderation_info"`
	AdministratorInfo   string `json:"administrator_info"`
	CreationReason      string `json:"creation_reason"`
	MaintenanceLifetime string `json:"maintenance_lifetime"`
	BusinessModel       string `json:"business_model"`
	HardwareInfo        string `json:"hardware_info"`
	SupportText         string `json:"support_text"`
}

// handleInstanceAbout returns the operator-authored about-page texts. Public,
// unauthenticated; all values are effective overlay values (raw markdown, ""
// when unset).
func (s *Server) handleInstanceAbout(c echo.Context) error {
	return c.JSON(http.StatusOK, instanceAboutResponse{
		Description:         s.settingString(instancesettings.KeyInstanceDescription, s.cfg.InstanceDescription),
		Terms:               s.settingString(instancesettings.KeyTerms, ""),
		CodeOfConduct:       s.settingString(instancesettings.KeyCodeOfConduct, ""),
		ModerationInfo:      s.settingString(instancesettings.KeyModerationInfo, ""),
		AdministratorInfo:   s.settingString(instancesettings.KeyAdministratorInfo, ""),
		CreationReason:      s.settingString(instancesettings.KeyCreationReason, ""),
		MaintenanceLifetime: s.settingString(instancesettings.KeyMaintenanceLifetime, ""),
		BusinessModel:       s.settingString(instancesettings.KeyBusinessModel, ""),
		HardwareInfo:        s.settingString(instancesettings.KeyHardwareInfo, ""),
		SupportText:         s.settingString(instancesettings.KeySupportText, ""),
	})
}
