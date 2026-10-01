import os
import pathlib
import subprocess
import tempfile

root = pathlib.Path.cwd()
env = dict(os.environ, PATH=str(root / ".bin") + os.pathsep + os.environ["PATH"])
version = os.environ["PROTOC_VERSION"]
if subprocess.check_output(["protoc", "--version"], text=True).strip() != f"libprotoc {version}":
    raise SystemExit(f"protoc {version} required; run via make")
with tempfile.TemporaryDirectory() as directory:
    subprocess.run(
        [
            "protoc",
            f"--go_out={directory}",
            "--go_opt=paths=source_relative",
            "--go_opt=Mapi/registration.proto=registration.local/frontend/api",
            f"--go-grpc_out={directory}",
            "--go-grpc_opt=paths=source_relative",
            "--go-grpc_opt=Mapi/registration.proto=registration.local/frontend/api",
            "api/registration.proto",
        ],
        env=env,
        check=True,
    )
    for name in ("registration.pb.go", "registration_grpc.pb.go"):
        if (root / "api" / name).read_bytes() != (
            pathlib.Path(directory) / "api" / name
        ).read_bytes():
            raise SystemExit(f"outdated {name}; run make proto-gen")
print("protobuf generation is current")
