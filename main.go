package main

import (
	"fmt"
	"net/http"
	"path/filepath"
	"runtime"
	"time"

	"github.com/cuigh/auxo/app"
	"github.com/cuigh/auxo/app/flag"
	_ "github.com/cuigh/auxo/cache/memory"
	"github.com/cuigh/auxo/config"
	"github.com/cuigh/auxo/data/valid"
	"github.com/cuigh/auxo/net/web"
	"github.com/cuigh/auxo/net/web/filter"
	"github.com/cuigh/auxo/net/web/filter/auth"
	"github.com/cuigh/auxo/net/web/renderer/jet"
	"github.com/cuigh/swirl/biz"
	"github.com/cuigh/swirl/controller"
	"github.com/cuigh/swirl/misc"
)

func main() {
	misc.BindOptions()

	app.Name = "Swirl"
	app.Version = "0.6.7"
	app.Desc = "A web management UI for Docker, focused on swarm cluster"
	app.Action = func(ctx *app.Context) {
		misc.LoadOptions()
		app.Run(server())
	}
	app.Flags.Register(flag.All)
	app.Start()
}

func server() *web.Server {
	setting, err := biz.Setting.Get()
	if err != nil {
		panic(fmt.Sprintf("Load setting failed: %v", err))
	}

	ws := web.Auto()

	// customize error handler
	ws.ErrorHandler.OnCode(http.StatusNotFound, func(ctx web.Context, err error) {
		if ctx.IsAJAX() {
			ctx.Status(http.StatusNotFound).HTML(http.StatusText(http.StatusNotFound)) // nolint: gas
		} else {
			ctx.Status(http.StatusNotFound).Render("404", nil) // nolint: gas
		}
	})
	ws.ErrorHandler.OnCode(http.StatusForbidden, func(ctx web.Context, err error) {
		if ctx.IsAJAX() {
			ctx.Status(http.StatusForbidden).HTML("You do not have permission to perform this operation") // nolint: gas
		} else {
			ctx.Status(http.StatusForbidden).Render("403", nil) // nolint: gas
		}
	})

	// set render
	ws.Validator = &valid.Validator{Tag: "valid"}
	ws.Renderer = jet.Must(jet.Debug(config.GetBool("debug")), jet.VarMap(misc.Funcs), jet.VarMap(map[string]interface{}{
		"language":   setting.Language,
		"version":    app.Version,
		"go_version": runtime.Version(),
		"time":       misc.FormatTime(setting.TimeZone.Offset),
		"i18n":       misc.Message(setting.Language),
	}))

	// register global filters
	ws.Use(filter.NewRecover())

	// register static handlers
	ws.Static("/assets", filepath.Join(filepath.Dir(app.Path()), "assets"))

	// create biz group
	form := &auth.Form{
		Identifier:        biz.User.Identify,
		Timeout:           time.Minute * 30,
		SlidingExpiration: true,
	}
	g := ws.Group("", form, filter.NewAuthorizer(biz.User.Authorize))

	// register auth handlers
	g.Post("/login", form.LoginJSON(biz.User.Login), web.WithName("login"), web.WithAuthorize(web.AuthAnonymous))
	g.Get("/logout", form.Logout, web.WithName("logout"), web.WithAuthorize(web.AuthAuthenticated))

	// register controllers
	g.Handle("", controller.Home())
	g.Handle("/profile", controller.Profile())
	g.Handle("/registry", controller.Registry())
	g.Handle("/node", controller.Node())
	g.Handle("/service", controller.Service(), biz.Perm)
	g.Handle("/service/template", controller.Template())
	g.Handle("/stack", controller.Stack())
	g.Handle("/network", controller.Network())
	g.Handle("/secret", controller.Secret())
	g.Handle("/config", controller.Config())
	g.Handle("/task", controller.Task())
	g.Handle("/container", controller.Container())
	g.Handle("/image", controller.Image())
	g.Handle("/volume", controller.Volume())
	g.Handle("/system/user", controller.User())
	g.Handle("/system/role", controller.Role())
	g.Handle("/system/setting", controller.Setting())
	g.Handle("/system/event", controller.Event())

	return ws
}
