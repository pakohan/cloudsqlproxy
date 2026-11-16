package main

import (
	"context"
	"sort"

	"cloud.google.com/go/auth"
	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/option"
	sqladmin "google.golang.org/api/sqladmin/v1beta4"
)

type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Instance struct {
	ConnectionName  string `json:"connectionName"`
	Name            string `json:"name"`
	Region          string `json:"region"`
	DatabaseVersion string `json:"databaseVersion"`
	State           string `json:"state"`
}

// ListProjects returns all active projects the credentials can see.
func ListProjects(ctx context.Context, creds *auth.Credentials) ([]Project, error) {
	svc, err := crm.NewService(ctx, option.WithAuthCredentials(creds))
	if err != nil {
		return nil, err
	}
	var projects []Project
	err = svc.Projects.List().Filter("lifecycleState:ACTIVE").Pages(ctx, func(resp *crm.ListProjectsResponse) error {
		for _, p := range resp.Projects {
			projects = append(projects, Project{ID: p.ProjectId, Name: p.Name})
		}
		return nil
	})
	sort.Slice(projects, func(i, j int) bool { return projects[i].ID < projects[j].ID })
	return projects, err
}

// ListInstances returns the Cloud SQL instances of a project.
func ListInstances(ctx context.Context, creds *auth.Credentials, project string) ([]Instance, error) {
	svc, err := sqladmin.NewService(ctx, option.WithAuthCredentials(creds))
	if err != nil {
		return nil, err
	}
	var instances []Instance
	err = svc.Instances.List(project).Pages(ctx, func(resp *sqladmin.InstancesListResponse) error {
		for _, i := range resp.Items {
			instances = append(instances, Instance{
				ConnectionName:  i.ConnectionName,
				Name:            i.Name,
				Region:          i.Region,
				DatabaseVersion: i.DatabaseVersion,
				State:           i.State,
			})
		}
		return nil
	})
	sort.Slice(instances, func(i, j int) bool { return instances[i].Name < instances[j].Name })
	return instances, err
}
