package main

import (
	"context"
	"net/http"
)

const PluginName = "bedrock-usage"

var HandlerRegisterer = registerer(PluginName)

type registerer string

func (r registerer) RegisterHandlers(register func(name string, handler func(context.Context, map[string]interface{}, http.Handler) (http.Handler, error))) {
	register(string(r), NewHandler)
}
