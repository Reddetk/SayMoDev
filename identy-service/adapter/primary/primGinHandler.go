// Package http
package http

import (
	"net/http"

	inport "github.com/Reddetk/SayMoDev/identy-service/port/in"

	"github.com/gin-gonic/gin"
)

type GinHandler struct {
	accAuth   inport.AccountAuthenticator
	blackList inport.
}

func NewGinRouter(svc inport.SumService, blackList inport.BlackListPort) *gin.Engine {
	handler := &GinHandler{
		svc:       svc,
		blackList: blackList,
	}

	router := gin.Default()
	router.Use(handler.blacklistMiddleware())
	router.GET("/sum", handler.sumHandler)
	return router
}

func (h *GinHandler) blacklistMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		jti := c.GetHeader("X-JTI")
		if jti != "" && h.blackList.IsBlackListed(jti) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "token is blacklisted"})
			return
		}
		c.Next()
	}
}

func (h *GinHandler) sumHandler(c *gin.Context) {
	aID := c.Query("a")
	bID := c.Query("b")

	if aID == "" || bID == "" {
		c.JSON(400, gin.H{"error": "query params 'a' and 'b' are required"})
		return
	}

	result, err := h.svc.GetAndSum(aID, bID)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"result": result})
}
