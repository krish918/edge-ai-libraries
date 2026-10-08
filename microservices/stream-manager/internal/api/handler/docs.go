// SPDX-FileCopyrightText: Copyright (C) 2026 Intel Corporation
// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	swaggerfiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	apidoc "github.com/open-edge-platform/edge-ai-libraries/microservices/stream-manager/docs/user-guide/api-docs"
)

func ServeSwaggerUI(c *gin.Context) {
	if strings.Trim(c.Param("any"), "/") == "openapi.yaml" {
		c.Data(http.StatusOK, "application/yaml; charset=utf-8", []byte(apidoc.OpenapiSpec))
		return
	}
	swaggerHandler := ginSwagger.WrapHandler(swaggerfiles.Handler, ginSwagger.URL("openapi.yaml"))
	swaggerHandler(c)
}
