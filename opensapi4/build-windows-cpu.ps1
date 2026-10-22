#requires -Version 5
# Builds the OpenSAPI4 .nstudio pack.
# Zero-config wrapper. Extra args pass through to build.py.
$ErrorActionPreference = "Stop"
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
python "$here\build.py" --os windows --arch amd64 @args
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
