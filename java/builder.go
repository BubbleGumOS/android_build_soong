// Copyright 2015 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package java

// This file generates the final rules for compiling all Java.  All properties related to
// compiling should have been translated into javaBuilderFlags or another argument to the Transform*
// functions.

import (
	"strings"

	"github.com/google/blueprint"

	"android/soong/android"
	"android/soong/java/config"
)

var (
	pctx = android.NewPackageContext("android/soong/java")

	// Compiling java is not conducive to proper dependency tracking.  The path-matches-class-name
	// requirement leads to unpredictable generated source file names, and a single .java file
	// will get compiled into multiple .class files if it contains inner classes.  To work around
	// this, all java rules write into separate directories and then a post-processing step lists
	// the files in the the directory into a list file that later rules depend on (and sometimes
	// read from directly using @<listfile>)
	javac = pctx.AndroidGomaStaticRule("javac",
		blueprint.RuleParams{
			Command: `rm -rf "$outDir" "$annoDir" && mkdir -p "$outDir" "$annoDir" && ` +
				`${config.JavacWrapper}${config.JavacCmd} ${config.JavacHeapFlags} ${config.CommonJdkFlags} ` +
				`$javacFlags $sourcepath $bootClasspath $classpath ` +
				`-source $javaVersion -target $javaVersion ` +
				`-d $outDir -s $annoDir @$out.rsp && ` +
				`${config.SoongZipCmd} -jar -o $out -C $outDir -D $outDir`,
			CommandDeps:    []string{"${config.JavacCmd}", "${config.SoongZipCmd}"},
			Rspfile:        "$out.rsp",
			RspfileContent: "$in",
		},
		"javacFlags", "sourcepath", "bootClasspath", "classpath", "outDir", "annoDir", "javaVersion")

	errorprone = pctx.AndroidStaticRule("errorprone",
		blueprint.RuleParams{
			Command: `rm -rf "$outDir" "$annoDir" && mkdir -p "$outDir" "$annoDir" && ` +
				`${config.ErrorProneCmd} ` +
				`$javacFlags $sourcepath $bootClasspath $classpath ` +
				`-source $javaVersion -target $javaVersion ` +
				`-d $outDir -s $annoDir @$out.rsp && ` +
				`${config.SoongZipCmd} -jar -o $out -C $outDir -D $outDir`,
			CommandDeps: []string{
				"${config.JavaCmd}",
				"${config.ErrorProneJavacJar}",
				"${config.ErrorProneJar}",
				"${config.SoongZipCmd}",
			},
			Rspfile:        "$out.rsp",
			RspfileContent: "$in",
		},
		"javacFlags", "sourcepath", "bootClasspath", "classpath", "outDir", "annoDir", "javaVersion")

	jar = pctx.AndroidStaticRule("jar",
		blueprint.RuleParams{
			Command:     `${config.SoongZipCmd} -jar -o $out $jarArgs`,
			CommandDeps: []string{"${config.SoongZipCmd}"},
		},
		"jarArgs")

	combineJar = pctx.AndroidStaticRule("combineJar",
		blueprint.RuleParams{
			Command:     `${config.MergeZipsCmd} -j $jarArgs $out $in`,
			CommandDeps: []string{"${config.MergeZipsCmd}"},
		},
		"jarArgs")

	desugar = pctx.AndroidStaticRule("desugar",
		blueprint.RuleParams{
			Command: `rm -rf $dumpDir && mkdir -p $dumpDir && ` +
				`${config.JavaCmd} ` +
				`-Djdk.internal.lambda.dumpProxyClasses=$$(cd $dumpDir && pwd) ` +
				`$javaFlags ` +
				`-jar ${config.DesugarJar} $classpathFlags $desugarFlags ` +
				`-i $in -o $out`,
			CommandDeps: []string{"${config.DesugarJar}"},
		},
		"javaFlags", "classpathFlags", "desugarFlags", "dumpDir")

	dx = pctx.AndroidStaticRule("dx",
		blueprint.RuleParams{
			Command: `rm -rf "$outDir" && mkdir -p "$outDir" && ` +
				`${config.DxCmd} --dex --output=$outDir $dxFlags $in && ` +
				`${config.SoongZipCmd} -o $outDir/classes.dex.jar -C $outDir -D $outDir && ` +
				`${config.MergeZipsCmd} -D -stripFile "*.class" $out $outDir/classes.dex.jar $in`,
			CommandDeps: []string{
				"${config.DxCmd}",
				"${config.SoongZipCmd}",
				"${config.MergeZipsCmd}",
			},
		},
		"outDir", "dxFlags")

	jarjar = pctx.AndroidStaticRule("jarjar",
		blueprint.RuleParams{
			Command:     "${config.JavaCmd} -jar ${config.JarjarCmd} process $rulesFile $in $out",
			CommandDeps: []string{"${config.JavaCmd}", "${config.JarjarCmd}", "$rulesFile"},
		},
		"rulesFile")
)

func init() {
	pctx.Import("android/soong/java/config")
}

type javaBuilderFlags struct {
	javacFlags    string
	dxFlags       string
	bootClasspath classpath
	classpath     classpath
	systemModules classpath
	desugarFlags  string
	aidlFlags     string
	javaVersion   string

	protoFlags   string
	protoOutFlag string
}

func TransformJavaToClasses(ctx android.ModuleContext, outputFile android.WritablePath,
	srcFiles android.Paths, srcJars classpath,
	flags javaBuilderFlags, deps android.Paths) {

	transformJavaToClasses(ctx, outputFile, srcFiles, srcJars, flags, deps,
		"", "javac", javac)
}

func RunErrorProne(ctx android.ModuleContext, outputFile android.WritablePath,
	srcFiles android.Paths, srcJars classpath,
	flags javaBuilderFlags) {

	if config.ErrorProneJar == "" {
		ctx.ModuleErrorf("cannot build with Error Prone, missing external/error_prone?")
	}

	transformJavaToClasses(ctx, outputFile, srcFiles, srcJars, flags, nil,
		"-errorprone", "errorprone", errorprone)
}

// transformJavaToClasses takes source files and converts them to a jar containing .class files.
// srcFiles is a list of paths to sources, srcJars is a list of paths to jar files that contain
// sources.  flags contains various command line flags to be passed to the compiler.
//
// This method may be used for different compilers, including javac and Error Prone.  The rule
// argument specifies which command line to use and desc sets the description of the rule that will
// be printed at build time.  The stem argument provides the file name of the output jar, and
// suffix will be appended to various intermediate files and directories to avoid collisions when
// this function is called twice in the same module directory.
func transformJavaToClasses(ctx android.ModuleContext, outputFile android.WritablePath,
	srcFiles android.Paths, srcJars classpath,
	flags javaBuilderFlags, deps android.Paths,
	intermediatesSuffix, desc string, rule blueprint.Rule) {

	deps = append(deps, srcJars...)

	var bootClasspath string
	if flags.javaVersion == "1.9" {
		deps = append(deps, flags.systemModules...)
		bootClasspath = flags.systemModules.JavaSystemModules(ctx.Device())
	} else {
		deps = append(deps, flags.bootClasspath...)
		bootClasspath = flags.bootClasspath.JavaBootClasspath(ctx.Device())
	}

	deps = append(deps, flags.classpath...)

	ctx.ModuleBuild(pctx, android.ModuleBuildParams{
		Rule:        rule,
		Description: desc,
		Output:      outputFile,
		Inputs:      srcFiles,
		Implicits:   deps,
		Args: map[string]string{
			"javacFlags":    flags.javacFlags,
			"bootClasspath": bootClasspath,
			"sourcepath":    srcJars.JavaSourcepath(),
			"classpath":     flags.classpath.JavaClasspath(),
			"outDir":        android.PathForModuleOut(ctx, "classes"+intermediatesSuffix).String(),
			"annoDir":       android.PathForModuleOut(ctx, "anno"+intermediatesSuffix).String(),
			"javaVersion":   flags.javaVersion,
		},
	})
}

func TransformResourcesToJar(ctx android.ModuleContext, outputFile android.WritablePath,
	jarArgs []string, deps android.Paths) {

	ctx.ModuleBuild(pctx, android.ModuleBuildParams{
		Rule:        jar,
		Description: "jar",
		Output:      outputFile,
		Implicits:   deps,
		Args: map[string]string{
			"jarArgs": strings.Join(jarArgs, " "),
		},
	})
}

func TransformJarsToJar(ctx android.ModuleContext, outputFile android.WritablePath,
	jars android.Paths, manifest android.OptionalPath, stripDirs bool) {

	var deps android.Paths

	var jarArgs []string
	if manifest.Valid() {
		jarArgs = append(jarArgs, "-m "+manifest.String())
		deps = append(deps, manifest.Path())
	}

	if stripDirs {
		jarArgs = append(jarArgs, "-D")
	}

	ctx.ModuleBuild(pctx, android.ModuleBuildParams{
		Rule:        combineJar,
		Description: "combine jars",
		Output:      outputFile,
		Inputs:      jars,
		Implicits:   deps,
		Args: map[string]string{
			"jarArgs": strings.Join(jarArgs, " "),
		},
	})
}

func TransformDesugar(ctx android.ModuleContext, outputFile android.WritablePath,
	classesJar android.Path, flags javaBuilderFlags) {

	dumpDir := android.PathForModuleOut(ctx, "desugar_dumped_classes")

	javaFlags := ""
	if ctx.AConfig().UseOpenJDK9() {
		javaFlags = "--add-opens java.base/java.lang.invoke=ALL-UNNAMED"
	}

	var desugarFlags []string
	desugarFlags = append(desugarFlags, flags.bootClasspath.DesugarBootClasspath()...)
	desugarFlags = append(desugarFlags, flags.classpath.DesugarClasspath()...)

	var deps android.Paths
	deps = append(deps, flags.bootClasspath...)
	deps = append(deps, flags.classpath...)

	ctx.ModuleBuild(pctx, android.ModuleBuildParams{
		Rule:        desugar,
		Description: "desugar",
		Output:      outputFile,
		Input:       classesJar,
		Implicits:   deps,
		Args: map[string]string{
			"dumpDir":        dumpDir.String(),
			"javaFlags":      javaFlags,
			"classpathFlags": strings.Join(desugarFlags, " "),
			"desugarFlags":   flags.desugarFlags,
		},
	})
}

// Converts a classes.jar file to classes*.dex, then combines the dex files with any resources
// in the classes.jar file into a dex jar.
func TransformClassesJarToDexJar(ctx android.ModuleContext, outputFile android.WritablePath,
	classesJar android.Path, flags javaBuilderFlags) {

	outDir := android.PathForModuleOut(ctx, "dex")

	ctx.ModuleBuild(pctx, android.ModuleBuildParams{
		Rule:        dx,
		Description: "dx",
		Output:      outputFile,
		Input:       classesJar,
		Args: map[string]string{
			"dxFlags": flags.dxFlags,
			"outDir":  outDir.String(),
		},
	})
}

func TransformJarJar(ctx android.ModuleContext, outputFile android.WritablePath,
	classesJar android.Path, rulesFile android.Path) {
	ctx.ModuleBuild(pctx, android.ModuleBuildParams{
		Rule:        jarjar,
		Description: "jarjar",
		Output:      outputFile,
		Input:       classesJar,
		Implicit:    rulesFile,
		Args: map[string]string{
			"rulesFile": rulesFile.String(),
		},
	})
}

type classpath []android.Path

// Returns a -sourcepath argument in the form javac expects.  If the list is empty returns
// -sourcepath "" to ensure javac does not fall back to searching the classpath for sources.
func (x *classpath) JavaSourcepath() string {
	if len(*x) > 0 {
		return "-sourcepath " + strings.Join(x.Strings(), ":")
	} else {
		return `-sourcepath ""`
	}
}

// Returns a -classpath argument in the form java or javac expects
func (x *classpath) JavaClasspath() string {
	if len(*x) > 0 {
		return "-classpath " + strings.Join(x.Strings(), ":")
	} else {
		return ""
	}
}

// Returns a -processorpath argument in the form java or javac expects
func (x *classpath) JavaProcessorpath() string {
	if len(*x) > 0 {
		return "-processorpath " + strings.Join(x.Strings(), ":")
	} else {
		return ""
	}
}

// Returns a -bootclasspath argument in the form java or javac expects.  If forceEmpty is true,
// returns -bootclasspath "" if the bootclasspath is empty to ensure javac does not fall back to the
// default bootclasspath.
func (x *classpath) JavaBootClasspath(forceEmpty bool) string {
	if len(*x) > 0 {
		return "-bootclasspath " + strings.Join(x.Strings(), ":")
	} else if forceEmpty {
		return `-bootclasspath ""`
	} else {
		return ""
	}
}

// Returns a --system argument in the form javac expects with -source 1.9.  If forceEmpty is true,
// returns --system=none if the list is empty to ensure javac does not fall back to the default
// system modules.
func (x *classpath) JavaSystemModules(forceEmpty bool) string {
	if len(*x) > 1 {
		panic("more than one system module")
	} else if len(*x) == 1 {
		return "--system=" + strings.TrimSuffix((*x)[0].String(), "lib/modules")
	} else if forceEmpty {
		return "--system=none"
	} else {
		return ""
	}
}

func (x *classpath) DesugarBootClasspath() []string {
	if x == nil || *x == nil {
		return nil
	}
	flags := make([]string, len(*x))
	for i, v := range *x {
		flags[i] = "--bootclasspath_entry " + v.String()
	}

	return flags
}

func (x *classpath) DesugarClasspath() []string {
	if x == nil || *x == nil {
		return nil
	}
	flags := make([]string, len(*x))
	for i, v := range *x {
		flags[i] = "--classpath_entry " + v.String()
	}

	return flags
}

// Append an android.Paths to the end of the classpath list
func (x *classpath) AddPaths(paths android.Paths) {
	for _, path := range paths {
		*x = append(*x, path)
	}
}

// Convert a classpath to an android.Paths
func (x *classpath) Paths() android.Paths {
	return append(android.Paths(nil), (*x)...)
}

func (x *classpath) Strings() []string {
	if x == nil {
		return nil
	}
	ret := make([]string, len(*x))
	for i, path := range *x {
		ret[i] = path.String()
	}
	return ret
}
