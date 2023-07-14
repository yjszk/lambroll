package lambroll

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	lambdav2 "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdav2types "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// RollbackOption represents option for Rollback()
type RollbackOption struct {
	FunctionFilePath *string
	DryRun           *bool
	DeleteVersion    *bool
}

func (opt RollbackOption) label() string {
	if *opt.DryRun {
		return "**DRY RUN**"
	}
	return ""
}

// Rollback rollbacks function
func (app *App) Rollback(opt RollbackOption) error {
	ctx := context.TODO()
	fn, err := app.loadFunctionV2(*opt.FunctionFilePath)
	if err != nil {
		return fmt.Errorf("failed to load function: %w", err)
	}

	log.Printf("[info] starting rollback function %s", *fn.FunctionName)

	res, err := app.lambdav2.GetAlias(ctx, &lambdav2.GetAliasInput{
		FunctionName: fn.FunctionName,
		Name:         aws.String(CurrentAliasName),
	})
	if err != nil {
		return fmt.Errorf("failed to get alias: %w", err)
	}

	currentVersion := *res.FunctionVersion
	cv, err := strconv.ParseInt(currentVersion, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to pase %s as int: %w", currentVersion, err)
	}

	var prevVersion string
VERSIONS:
	for v := cv - 1; v > 0; v-- {
		log.Printf("[debug] get function version %d", v)
		vs := strconv.FormatInt(v, 10)
		res, err := app.lambdav2.GetFunction(ctx, &lambdav2.GetFunctionInput{
			FunctionName: fn.FunctionName,
			Qualifier:    aws.String(vs),
		})
		if err != nil {
			var nfe *lambdav2types.ResourceNotFoundException
			if errors.As(err, &nfe) {
				log.Printf("[debug] version %s not found", vs)
				continue VERSIONS
			} else {
				return fmt.Errorf("failed to get function: %w", err)
			}
		}
		prevVersion = *res.Configuration.Version
		break
	}
	if prevVersion == "" {
		return errors.New("unable to detect previous version of function")
	}

	log.Printf("[info] rollbacking function version %s to %s %s", currentVersion, prevVersion, opt.label())
	if *opt.DryRun {
		return nil
	}
	err = app.updateAliases(*fn.FunctionName, versionAlias{Version: prevVersion, Name: CurrentAliasName})
	if err != nil {
		return err
	}

	if !*opt.DeleteVersion {
		return nil
	}

	return app.deleteFunctionVersion(*fn.FunctionName, currentVersion)
}

func (app *App) deleteFunctionVersion(functionName, version string) error {
	ctx := context.TODO()
	for {
		log.Printf("[debug] checking aliased version")
		res, err := app.lambdav2.GetAlias(ctx, &lambdav2.GetAliasInput{
			FunctionName: aws.String(functionName),
			Name:         aws.String(CurrentAliasName),
		})
		if err != nil {
			return fmt.Errorf("failed to get alias: %w", err)
		}
		if *res.FunctionVersion == version {
			log.Printf("[debug] version %s still has alias %s, retrying", version, CurrentAliasName)
			time.Sleep(time.Second)
			continue
		}
		break
	}
	log.Printf("[info] deleting function version %s", version)
	_, err := app.lambdav2.DeleteFunction(ctx, &lambdav2.DeleteFunctionInput{
		FunctionName: aws.String(functionName),
		Qualifier:    aws.String(version),
	})
	if err != nil {
		return fmt.Errorf("failed to delete version: %w", err)
	}
	return nil
}
