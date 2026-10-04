"""Rasterize a native Word-exported PDF for document visual verification.

The caller supplies convert_to_pdf. No external model or private configuration
is consulted. Install pdf2image and put Poppler on PATH for this optional tool.
"""
from pathlib import Path
import tempfile


def convert_to_pdf(doc_path, user_profile, convert_tmp_dir, stem, verbose=False):
    raise RuntimeError("Supply a native Word/LibreOffice PDF conversion callback")


def rasterize(doc_path, output_dir, dpi=130, *unused):
    from pdf2image import convert_from_path
    destination = Path(output_dir)
    destination.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="factorforge-render-") as temporary:
        pdf, _ = convert_to_pdf(doc_path, None, temporary, Path(doc_path).stem)
        pages = convert_from_path(pdf, dpi=dpi)
        result = []
        for index, page in enumerate(pages, 1):
            target = destination / f"page-{index}.png"
            page.save(target)
            result.append(str(target))
        return result
