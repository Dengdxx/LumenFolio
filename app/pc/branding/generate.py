"""Generate the Gio heading image and PE icon resources from the brand icon."""

from pathlib import Path
import shutil
import subprocess
import tempfile

from PIL import Image


def main() -> None:
    branding = Path(__file__).resolve().parent
    pc = branding.parent
    source = pc.parents[1] / "assets" / "branding" / "lumen-folio-icon.png"
    machines = {"386": "X86", "amd64": "X64", "arm64": "ARM64"}
    sizes = [(n, n) for n in (16, 24, 32, 48, 64, 128, 256)]

    with tempfile.TemporaryDirectory(prefix="lumen-folio-icon-") as temp:
        work = Path(temp)
        with Image.open(source) as image:
            image.save(work / "icon.ico", sizes=sizes)
        # Gio 读取 Windows 的 IDI_APPLICATION（32512）。
        (work / "icon.rc").write_text('32512 ICON "icon.ico"\n', encoding="ascii")
        subprocess.run(["llvm-rc", "/fo", "icon.res", "icon.rc"], cwd=work, check=True)
        for arch, machine in machines.items():
            output = f"icon_windows_{arch}.syso"
            subprocess.run(
                ["llvm-cvtres", f"/machine:{machine}", "/timestamp:0", f"/out:{output}", "icon.res"],
                cwd=work,
                check=True,
            )
        shutil.copyfile(source, branding / source.name)
        for arch in machines:
            output = f"icon_windows_{arch}.syso"
            shutil.copyfile(work / output, pc / output)


if __name__ == "__main__":
    main()
