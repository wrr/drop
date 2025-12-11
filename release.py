#!/usr/bin/env python3

# Copyright 2025 Jan Wrobel <jan@mixedbit.org>
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http:#www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Release script for Drop"""

import subprocess
import sys
import re
import os
import shutil

from pathlib import Path

class ReleaseError(Exception):
    pass

def run(cmd, check=True, capture_output=False):
    print(f'\nRunning: {cmd}')
    return subprocess.run(
        cmd,
        shell=True,
        capture_output=capture_output,
        text=True,
        check=check
    )

step_nr = 0
def step(msg):
    global step_nr
    step_nr += 1
    format_bold = '\033[1m'
    format_reset = '\033[0m'
    print(f'\n{format_bold}({step_nr}) {msg}: {format_reset}')

def prompt(msg):
    print(f'\n{msg}')
    return input('> ').strip()

def version_to_tag(version):
    return f'v{version}'

def check_prerequisites():
    step('Checking prerequisites')
    if shutil.which('gh') is None:
        raise ReleaseError(
            'GitHub CLI (gh) not found. Install it with:\n'
            '  sudo apt install gh  # Debian/Ubuntu\n'
            'or see: https://cli.github.com/'
        )
    run('gh --version')

    result = run('gh auth status', check=False)
    if result.returncode != 0:
        raise ReleaseError(
            'Not authenticated with GitHub CLI. Run:\n'
            '  gh auth login'
        )

def run_all_tests_and_checks():
    step('Running all tests and checks')
    run('make all', capture_output=False)

def ensure_main_branch():
    step('Ensuring the main branch is active')
    result = run('git branch --show-current', capture_output=True)
    current_branch = result.stdout.strip()

    if current_branch != 'main':
        raise ReleaseError("Release must be made from the 'main' branch.")

def ensure_nothing_to_commit():
    step('Ensuring repository has no uncommited changes')
    result = run('git status --porcelain', capture_output=True)

    if result.stdout.strip():
        print(f'Uncommitted changes found:\n{result.stdout}', file=sys.stderr)
        raise ReleaseError('Repository has uncommitted changes.')

def prompt_for_new_version():
    step('Establishing version number to use for the release')

    result = run('git describe --tags --abbrev=0', capture_output=True,
                 check=False)
    if result.returncode == 0:
        latest_tag = result.stdout.strip()
        latest_version = latest_tag.removeprefix('v')
    else:
        latest_version = '0.1.0'

    new_version = prompt(f"Enter new version number 'N1.N2.N3' "
                         f"(latest {latest_version})")

    if not re.match(r'^\d+\.\d+\.\d+(-[a-zA-Z0-9.-]+)?$', new_version):
        raise ReleaseError(
            f"Invalid version '{new_version}'. "
            "Version must follow semver format (e.g., 1.2.3)")

    new_tag = version_to_tag(new_version)
    result = run(f'git tag -l {new_tag}', capture_output=True)
    if result.stdout.strip():
        raise ReleaseError(f"Tag '{new_tag}' already exists")

    return new_version

def add_git_tag_and_push(version):
    tag = version_to_tag(version)
    step(f'Adding tag {tag}')

    release_notes = f'Release {tag}'
    run(f"git tag -a {tag} -m '{release_notes}'")
    step('Pushing tag and local changes to origin')
    run(f'git push origin {tag}')

def build_binaries(version):
    step('Building release binaries')

    dist_dir = Path('dist')
    if dist_dir.exists():
        shutil.rmtree(dist_dir)

    run('make build-release', capture_output=False)

    binaries = list(dist_dir.glob('drop*'))
    if not binaries:
        raise ReleaseError('No binaries found in dist/')

    print('\nBuilt binaries:')
    for binary in binaries:
        size = binary.stat().st_size / (1024 * 1024)
        print(f'  - {binary.name} ({size:.2f} MB)')

def create_github_release(version):
    step('Creating GitHub release')
    tag = version_to_tag(version)

    cmd = (f"gh release create {tag} --title 'Release {version}' "
           "dist/drop* LICENSE NOTES")
    run(cmd, capture_output=False)
    print(f'\nGitHub release created: {version}')

    result = run(f'gh release view {tag} --json url -q .url',
                 capture_output=True)
    release_url = result.stdout.strip()
    return release_url

def main():
    try:
        os.chdir(Path(__file__).parent)
        check_prerequisites()
        ensure_main_branch()
        run_all_tests_and_checks()
        ensure_nothing_to_commit()
        version = prompt_for_new_version()

        if prompt(f'\nAll checks passed. Ready to release version {version}\n'
                  'Proceed? [y/N]:') != 'y':
            print('Release cancelled')
            return 1

        add_git_tag_and_push(version)
        build_binaries(version)
        release_url = create_github_release(version)
        print(f'\nRelease created: {release_url}')
        return 0

    except (ReleaseError, subprocess.SubprocessError) as e:
        print(f'Error: {e}', file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        print("Release cancelled")
        return 1


if __name__ == "__main__":
    sys.exit(main())
