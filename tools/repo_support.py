"""Git helpers use command-scoped trust, including Windows UNC checkouts."""
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def git(root, *arguments):
    command = ["git", "-c", f"safe.directory={Path(root).as_posix()}",
               "-C", str(root), *arguments]
    result = subprocess.run(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if result.returncode:
        # Do not echo command output: it could contain private paths or values.
        raise RuntimeError(f"Git operation failed: {arguments[0]}")
    return result.stdout


def tracked_blobs(root, revision=None):
    if revision is None:
        records = git(root, "ls-files", "--stage", "-z").split(b"\0")
        for record in records:
            if record:
                metadata, path = record.split(b"\t", 1)
                mode, sha, stage = metadata.split()
                if stage != b"0":
                    raise RuntimeError("Unmerged index; resolve it before checking")
                yield path.decode("utf-8"), sha.decode("ascii"), mode.decode("ascii")
    else:
        records = git(root, "ls-tree", "-r", "-z", revision).split(b"\0")
        for record in records:
            if record:
                metadata, path = record.split(b"\t", 1)
                mode, kind, sha = metadata.split()
                if kind == b"blob":
                    yield path.decode("utf-8"), sha.decode("ascii"), mode.decode("ascii")
                else:
                    raise RuntimeError("Embedded repositories require separate publication review")
