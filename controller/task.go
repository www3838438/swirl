package controller

import (
	"github.com/cuigh/auxo/net/web"
	"github.com/cuigh/swirl/biz/docker"
	"github.com/cuigh/swirl/misc"
	"github.com/cuigh/swirl/model"
)

// TaskController is a controller of swarm task
type TaskController struct {
	List   web.HandlerFunc `path:"/" name:"task.list" authorize:"!" desc:"task list page"`
	Detail web.HandlerFunc `path:"/:id/detail" name:"task.detail" authorize:"!" desc:"task detail page"`
	Raw    web.HandlerFunc `path:"/:id/raw" name:"task.raw" authorize:"!" desc:"task raw page"`
}

// Task creates an instance of TaskController
func Task() (c *TaskController) {
	return &TaskController{
		List:   taskList,
		Detail: taskDetail,
		Raw:    taskRaw,
	}
}

func taskList(ctx web.Context) error {
	args := &model.TaskListArgs{}
	err := ctx.Bind(args)
	if err != nil {
		return err
	}
	args.PageSize = model.PageSize
	if args.PageIndex == 0 {
		args.PageIndex = 1
	}

	tasks, totalCount, err := docker.TaskList(args)
	if err != nil {
		return err
	}

	m := newPagerModel(ctx, totalCount, args.PageSize, args.PageIndex).
		Set("Args", args).
		Set("Tasks", tasks)
	return ctx.Render("task/list", m)
}

func taskDetail(ctx web.Context) error {
	id := ctx.P("id")
	task, _, err := docker.TaskInspect(id)
	if err != nil {
		return err
	}

	m := newModel(ctx).Set("Task", task)
	return ctx.Render("task/detail", m)
}

func taskRaw(ctx web.Context) error {
	id := ctx.P("id")
	task, raw, err := docker.TaskInspect(id)
	if err != nil {
		return err
	}

	j, err := misc.JSONIndent(raw)
	if err != nil {
		return err
	}

	m := newModel(ctx).Set("Task", task).Set("Raw", j)
	return ctx.Render("task/raw", m)
}
