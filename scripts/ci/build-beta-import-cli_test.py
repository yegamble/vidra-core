"""Exercise publisher refusal paths before a compiler or importer can run."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("build-beta-import-cli.sh")
SHA = "a" * 40


class BetaBuildTests(unittest.TestCase):
    def run_case(self, existing_output=False, **changes):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            tools = root / "tools"
            tools.mkdir()
            (tools / "git").write_text("""#!/bin/sh
case "$*" in
  'rev-parse HEAD') printf '%s\n' "$FIXTURE_HEAD";;
  'rev-parse HEAD^{tree}') printf '%040d\n' 1;;
  rev-parse*) printf '%s\n' "$FIXTURE_TAG";;
  status*) printf '%s' "$FIXTURE_DIRTY";;
  *) exit 91;;
esac
""")
            (tools / "go").write_text(r"""#!/usr/bin/env python3
import json,os,pathlib,sys
p=pathlib.Path(os.environ['COMPILER_LOG']);p.write_text(p.read_text()+'called\n' if p.exists() else 'called\n')
if sys.argv[1]=='build':
 target=pathlib.Path(sys.argv[sys.argv.index('-o')+1]);target.write_text('#!/bin/sh\nexit '+os.environ['FIXTURE_HELP_EXIT']+'\n');target.chmod(0o755)
else:
 settings={'GOOS':'linux','GOARCH':os.environ['FIXTURE_ARCH'],'CGO_ENABLED':'0','-trimpath':'true','vcs.revision':os.environ['FIXTURE_REVISION'],'vcs.modified':os.environ['FIXTURE_MODIFIED']}
 print(json.dumps({'GoVersion':'go1.27.3','Path':'github.com/vidra/vidra-core/cmd/peertube-import','Settings':[{'Key':k,'Value':v} for k,v in settings.items()]}))
""")
            for tool in tools.iterdir():
                tool.chmod(0o755)
            if existing_output:
                (root/"out").mkdir()
                (root/"out"/"keep").write_bytes(b"preserved\n")
            tag = changes.get("BETA_TAG","beta-fixture")
            env = dict(os.environ, PATH=str(tools)+os.pathsep+os.environ["PATH"],
                       BETA_TAG=tag, EXPECTED_SHA=SHA, GITHUB_SHA=SHA,
                       GITHUB_REF="refs/tags/"+tag, GITHUB_REPOSITORY="yegamble/vidra-core",
                       GITHUB_WORKFLOW_REF="yegamble/vidra-core/.github/workflows/publish-beta-import-cli.yml@refs/tags/"+tag,
                       WORKFLOW_SHA=SHA, GITHUB_RUN_ID="1", GITHUB_RUN_ATTEMPT="1", OUT_DIR=str(root/"out"),
                       COMPILER_LOG=str(root/"compiler.log"), FIXTURE_HEAD=SHA, FIXTURE_TAG=SHA, FIXTURE_DIRTY="",
                       FIXTURE_ARCH="amd64", FIXTURE_REVISION=SHA, FIXTURE_MODIFIED="false", FIXTURE_HELP_EXIT="0")
            env.update(changes)
            result = subprocess.run(["bash", str(SCRIPT)], cwd=SCRIPT.parents[2], env=env,
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
            called = (root/"compiler.log").exists()
            output = root/"out"
            names = sorted(path.name for path in output.iterdir()) if output.exists() else []
            manifest = json.loads((output/"provenance.json").read_text()) if (output/"provenance.json").exists() else None
            if existing_output:
                self.assertEqual((output/"keep").read_bytes(),b"preserved\n")
            return result.returncode, called, names, manifest

    def test_existing_output_is_preserved_without_compiler(self):
        status,called,names,_ = self.run_case(existing_output=True)
        self.assertNotEqual(status,0)
        self.assertFalse(called)
        self.assertEqual(names,["keep"])

    def test_refusals_precede_compiler(self):
        for change in ({"BETA_TAG":"v1.2.3"}, {"BETA_TAG":"beta-$(false)"}, {"GITHUB_REF":"refs/heads/main"},
                       {"EXPECTED_SHA":"main"}, {"GITHUB_SHA":"b"*40}, {"WORKFLOW_SHA":"b"*40},
                       {"FIXTURE_HEAD":"b"*40}, {"FIXTURE_TAG":"b"*40}, {"GITHUB_WORKFLOW_REF":"wrong"}, {"FIXTURE_DIRTY":" M go.mod"}):
            with self.subTest(change=change):
                status,called,_,_ = self.run_case(**change)
                self.assertNotEqual(status,0)
                self.assertFalse(called)

    def test_bad_binary_or_help_refuses_publication(self):
        for change in ({"FIXTURE_ARCH":"arm64"}, {"FIXTURE_REVISION":"b"*40},
                       {"FIXTURE_MODIFIED":"true"}, {"FIXTURE_HELP_EXIT":"7"}):
            with self.subTest(change=change):
                status,_,names,_ = self.run_case(**change)
                self.assertNotEqual(status,0)
                self.assertNotIn("SHA256SUMS",names)

    def test_exact_outputs_and_provenance(self):
        status,called,names,manifest = self.run_case()
        self.assertEqual(status,0)
        self.assertTrue(called)
        self.assertEqual(names,["SHA256SUMS","buildinfo.json","peertube-import_beta-fixture_linux_amd64","provenance.json"])
        self.assertEqual(manifest["source_commit"],SHA)
        self.assertEqual(manifest["tag"],"beta-fixture")


if __name__ == "__main__":
    unittest.main()
