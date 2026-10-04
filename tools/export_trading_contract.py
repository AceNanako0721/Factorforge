"""Export the installed P1 API contract without loading private configuration."""
import json
from pathlib import Path

from factorforge.trading.adapters.memory import MemoryStore
from factorforge.trading.api.app import create_app
from factorforge.trading.application.commands import TradingService

ROOT = Path(__file__).resolve().parents[1]


def main():
    destination = ROOT / "contracts/v2/trading/openapi.json"
    destination.parent.mkdir(parents=True, exist_ok=True)
    app = create_app(TradingService(MemoryStore()), {})
    destination.write_text(json.dumps(app.openapi(), ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print("Exported trading-2.0 contract")


if __name__ == "__main__":
    main()
