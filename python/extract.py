#!/usr/bin/env python3
"""
Universal PDF Extractor (FINAL FIXED v2)
- Trade Audit + Market Info
- Fix numeric shift bug (TradeID ikut kebaca sebagai number)
- Stable mapping: TradeVol, Price, CloseVol, CloseSettle, Fee, Overnight
- Safe JSON (Go-ready)
"""

import sys, json, re
from pathlib import Path

try:
    import pdfplumber
except Exception as e:
    print(json.dumps({"success": False, "error": str(e)}))
    sys.exit(0)

# ================= REGEX =================
DATE  = re.compile(r"^\d{4}-\d{2}-\d{2}$")
TIME  = re.compile(r"^\d{2}:\d{2}:\d{2}$")
MONTH = re.compile(r"^[A-Z]{3}\d{2}$")
NUM   = re.compile(r"^\d+(\.\d+)?$")


# ================= UTILS =================
def norm(v):
    return re.sub(r"\s+", " ", str(v)).strip() if v else ""


# ================= LINE BUILDER =================
class LineBuilder:
    @staticmethod
    def build(page):
        words = page.extract_words(x_tolerance=2, y_tolerance=3)
        lines = {}
        for w in words:
            y = round(w["top"], 1)
            lines.setdefault(y, []).append(w)
        return [
            [norm(w["text"]) for w in sorted(lines[y], key=lambda x: x["x0"])]
            for y in sorted(lines)
        ]


# ================= TEXT PARSER =================
class TextParser:
    def parse(self, page, page_number):
        text = page.extract_text()
        if not text:
            return None
        return {
            "type": "text",
            "content": text,
            "page_number": page_number,
            "confidence": 1.0,
            "metadata": {"source": "pdfplumber"}
        }


# ================= TRADE PARSER =================
class TradeParser:
    HEADERS = [
        "DateTime", "TradeID", "Contract", "Acc", "BuySell",
        "TradeVol", "Price", "CloseVol", "CloseSettle", "Fee", "Overnight"
    ]

    def can_parse(self, lines):
        return any("DateTrade" in " ".join(r) for r in lines)

    def parse(self, lines, page_number):
        grouped, buffer = [], []

        # ---- GROUP ROWS ----
        for r in lines:
            if not r:
                continue

            joined = " ".join(r)

            # skip header lines
            if "DateTrade" in joined:
                continue
            if joined.strip().lower().startswith("report audit trade"):
                continue
            # header continuation often like: "Sell Vol Vol Settle Trade"
            if ("vol" in joined.lower() and "settle" in joined.lower() and "trade" in joined.lower()
                and not DATE.match(r[0])):
                continue

            # new record
            if DATE.match(r[0]):
                if buffer:
                    grouped.append(buffer)
                buffer = r[:]
                continue

            # continuation line: "22:32:30 AUG25" or "AUG25"
            if buffer and (TIME.match(r[0]) or (len(r) == 1 and MONTH.match(r[0]))):
                buffer.extend(r)

        if buffer:
            grouped.append(buffer)

        fixed = []

        for r in grouped:
            # basic sanity (need at least date, trade_id, contract, acc, side)
            if len(r) < 5 or not DATE.match(r[0]):
                continue

            date = r[0]
            time = next((x for x in r if TIME.match(x)), "00:00:00")
            datetime = f"{date} {time}"

            trade_id = r[1]
            contract = r[2]
            acc      = r[3]
            side     = r[4]

            # append month to contract if CPOID-
            month = next((x for x in r if MONTH.match(x)), None)
            if contract == "CPOID-":
                contract = f"CPOID-{month}" if month else "CPOID-NaN"

            # ---- FIXED: SCAN VALUES ONLY AFTER SIDE ----
            # expected segment after side: TradeVol, Price, CloseVol, (C/O), Fee, Overnight
            tail = r[5:] if len(r) > 5 else []

            nums = []
            settle = None

            for x in tail:
                if not x:
                    continue
                x = str(x).strip()
                if not x:
                    continue

                # Close settle marker
                if x in ("C", "O"):
                    settle = x
                    continue

                # numeric values (trade_vol, price, close_vol, fee, overnight)
                if NUM.match(x):
                    nums.append(x)

            # ---- MAP SAFELY (stable order) ----
            trade_vol = nums[0] if len(nums) > 0 else "NaN"
            price     = nums[1] if len(nums) > 1 else "NaN"
            close_vol = nums[2] if len(nums) > 2 else "NaN"
            fee       = nums[3] if len(nums) > 3 else "0.00"
            overnight = nums[4] if len(nums) > 4 else "0.00"

            fixed.append([
                datetime,
                trade_id,
                contract,
                acc,
                side,
                trade_vol,
                price,
                close_vol,
                settle if settle else "NaN",
                fee,
                overnight
            ])

        if not fixed:
            return None

        return {
            "type": "table",
            "content": {
                "headers": self.HEADERS,
                "rows": fixed,
                "row_count": len(fixed),
                "column_count": len(self.HEADERS)
            },
            "page_number": page_number,
            "confidence": 0.99,
            "metadata": {"source": "trade-parser-final-v2"}
        }


# ================= MARKET INFO PARSER =================
class MarketInfoParser:
    def can_parse(self, lines):
        return any("PreviousPrice" in " ".join(r) for r in lines)

    def parse(self, lines, page_number):
        headers, rows = None, []
        contract = date = nums = None

        for r in lines:
            if not r:
                continue

            if not headers and "PreviousPrice" in " ".join(r):
                headers = ["Contract", "Date"] + r[1:]
                continue

            if r[0] == "CPOID-":
                contract = "CPOID-"
                date = r[1]
                nums = r[2:]
                continue

            if contract and len(r) == 1 and MONTH.match(r[0]):
                row = [f"{contract}{r[0]}", date] + nums
                while len(row) < len(headers):
                    row.append("NaN")
                rows.append(row[:len(headers)])
                contract = None

        if not headers or not rows:
            return None

        return {
            "type": "table",
            "content": {
                "headers": headers,
                "rows": rows,
                "row_count": len(rows),
                "column_count": len(headers)
            },
            "page_number": page_number,
            "confidence": 0.97,
            "metadata": {"source": "market-info-parser"}
        }


# ================= EXTRACTOR =================
class PDFExtractor:
    def __init__(self, pdf_path):
        self.pdf_path = Path(pdf_path)
        self.parsers = [TradeParser(), MarketInfoParser()]
        self.text_parser = TextParser()
        self.items = []

    def extract(self):
        with pdfplumber.open(self.pdf_path) as pdf:
            for i, page in enumerate(pdf.pages, 1):
                t = self.text_parser.parse(page, i)
                if t:
                    self.items.append(t)

                lines = LineBuilder.build(page)
                for p in self.parsers:
                    if p.can_parse(lines):
                        item = p.parse(lines, i)
                        if item:
                            self.items.append(item)


# ================= MAIN =================
def main():
    if len(sys.argv) < 2:
        print(json.dumps({"success": False, "error": "no file"}))
        return

    extractor = PDFExtractor(sys.argv[1])
    extractor.extract()

    print(json.dumps({
        "success": True,
        "data": extractor.items
    }, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
