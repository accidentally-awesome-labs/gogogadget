package modkit

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// ToolRunner executes a fixed argv in a project root; manifests never carry
// shell fragments.
type ToolRunner interface { Run(context.Context, string, []string) error }

type OSCommandRunner struct{}
func (OSCommandRunner) Run(ctx context.Context, root string, argv []string) error {
	if len(argv)==0 { return fmt.Errorf("tool argv is empty") }
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir=root
	cmd.Env=os.Environ()
	return cmd.Run()
}

// EffectiveDependencies chooses the greatest declared version per module path
// and rejects conflicting immutable tool/container metadata.
func EffectiveDependencies(modules []Manifest) (Dependencies, error) {
	var out Dependencies
	out.Go=[]GoDependency{}; out.Tools=[]ToolArtifact{}; out.Containers=[]ContainerDependency{}
	goVersions:=map[string]string{}
	for _, m := range modules {
		for _, d := range m.Dependencies.Go {
			if err:=module.CheckPath(d.Module); err!=nil{return Dependencies{},fmt.Errorf("%s: %w",d.Module,err)}
			if old,ok:=goVersions[d.Module]; !ok || semver.Compare(old,d.Version)<0 { goVersions[d.Module]=d.Version }
		}
		out.Tools=append(out.Tools,m.Dependencies.Tools...); out.Containers=append(out.Containers,m.Dependencies.Containers...)
	}
	for p,v:=range goVersions { out.Go=append(out.Go,GoDependency{Module:p,Version:v}) }; sort.Slice(out.Go,func(i,j int)bool{return out.Go[i].Module<out.Go[j].Module})
	if err:=mergeTools(out.Tools); err!=nil{return Dependencies{},err}; if err:=mergeContainers(out.Containers);err!=nil{return Dependencies{},err}
	return out,nil
}
func mergeTools(all []ToolArtifact) error { seen:=map[string]ToolArtifact{}; for _,t:=range all { if old,ok:=seen[t.InstallPath];ok&&old!=t{return fmt.Errorf("conflicting tool artifact %q",t.InstallPath)};seen[t.InstallPath]=t }; return nil }
func mergeContainers(all []ContainerDependency) error { seen:=map[string]string{}; for _,c:=range all {if old,ok:=seen[c.Name];ok&&old!=c.Image{return fmt.Errorf("conflicting container %q",c.Name)};seen[c.Name]=c.Image};return nil }

// UpdateGoMod journals managed dependency ownership and rolls go.mod/go.sum
// back when the injected tool runner fails.
func UpdateGoMod(ctx context.Context, root string, deps []LockedDependency, runner ToolRunner) ([]LockedDependency,error) {
	modPath:=filepath.Join(root,"go.mod"); sumPath:=filepath.Join(root,"go.sum"); oldMod,err:=os.ReadFile(modPath);if err!=nil{return nil,err}; oldSum,_:=os.ReadFile(sumPath)
	file,err:=modfile.Parse(modPath,oldMod,nil);if err!=nil{return nil,err}
	for _,d:=range deps {
		var cur *modfile.Require
		for _, req := range file.Require { if req.Mod.Path == d.Module { cur = req; break } }
		if cur==nil {if err:=file.AddRequire(d.Module,d.ManagedVersion);err!=nil{return nil,err}}
	}
	newMod,err:=file.Format();if err!=nil{return nil,err};if err=os.WriteFile(modPath,newMod,0o644);err!=nil{return nil,err}
	if runner!=nil {if err:=runner.Run(ctx,root,[]string{"go","mod","download"});err!=nil { _=os.WriteFile(modPath,oldMod,0o644); _=os.WriteFile(sumPath,oldSum,0o644); return nil,err }}
	return deps,nil
}
