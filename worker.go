package main

import (
	"context"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/prometheus/client_golang/prometheus"
)

func (c *Config) HealthCheckWorker(ctx context.Context, done chan<- interface{}) {
	var sleepCounter int
	for {
		select {
		case <-ctx.Done():
			done <- nil
			return
		default:
			if sleepCounter/p.HealthCheckInterval == 0 {
				break
			}
			exist, err := c.minioClient.BucketExists(ctx, p.S3BucketName)
			if err != nil || !exist {
				backendState = StateUnhealthy
			} else {
				// wait HealthCheckReturnGap before declaring the services as OK
				if backendState == StateUnhealthy {
					time.Sleep(p.HealthCheckReturnGap)
				}
				backendState = StateHealthy
			}
			sleepCounter = 0
		}
		sleepCounter++
		time.Sleep(1 * time.Second)
	}
}

func (c *Config) CleanupWorker(ctx context.Context, done chan<- interface{}) {
	var sleepCounter int
	for {
		select {
		case <-ctx.Done():
			done <- nil
			return
		default:
			if sleepCounter/p.CleanupInterval == 0 {
				break
			}
			for object := range c.minioClient.ListObjects(ctx, p.S3BucketName, minio.ListObjectsOptions{Recursive: true}) {
				if object.LastModified.Add(1 * time.Hour).Before(time.Now()) {
					c.logger.Printf("[cleanup] - remove %+v\n", object.Key)
					metricObjectAction.With(prometheus.Labels{"action": "delete"}).Inc()
					if err := c.minioClient.RemoveObject(ctx, p.S3BucketName, object.Key, minio.RemoveObjectOptions{}); err != nil {
						c.logger.Println(err)
					}
				}
			}
			sleepCounter = 0
		}
		sleepCounter++
		time.Sleep(1 * time.Second)
	}
}
