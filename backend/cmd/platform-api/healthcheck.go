package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// healthcheckHandler reports server status, environment and build version.
// GET /v1/healthcheck
func (app *application) healthcheckHandler(c *gin.Context) {
	data := envelope{
		"status": "available",
		"system_info": envelope{
			"environment": app.config.env,
			"version":     buildVersion(),
			"build_time":  buildTime,
		},
	}

	err := app.writeJSON(c, http.StatusOK, data, nil)
	if err != nil {
		app.serverErrorResponse(c, err)
	}
}
