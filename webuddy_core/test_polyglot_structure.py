import tempfile
import unittest
from pathlib import Path

from tmh.polyglot_structure import build_polyglot_structure, extract_polyglot_proposal


class TestPolyglotStructure(unittest.TestCase):
    def test_multi_language_ecosystems_and_tmh_graph_validation(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)

            # 1. TypeScript / NestJS + Next.js App Router + Prisma
            (root / "prisma").mkdir()
            (root / "prisma" / "schema.prisma").write_text(
                "model Invoice {\n  id Int @id\n}\n", encoding="utf-8"
            )
            (root / "src").mkdir()
            (root / "src" / "invoice.controller.ts").write_text(
                "@Controller('api/invoices')\n"
                "export class InvoiceController {\n"
                "  @Post('approve')\n"
                "  approve() { return Invoice.update({}); }\n"
                "}\n",
                encoding="utf-8",
            )
            (root / "app" / "api" / "health").mkdir(parents=True)
            (root / "app" / "api" / "health" / "route.ts").write_text(
                "export async function GET() { return Response.json({ ok: true }); }\n",
                encoding="utf-8",
            )

            # 2. C# / .NET ASP.NET Core + EF Core
            (root / "dotnet").mkdir()
            (root / "dotnet" / "OrdersController.cs").write_text(
                '[Route("api/[controller]")]\n'
                "public class OrdersController {\n"
                '  [HttpPost("create")]\n'
                "  public void Create() { Orders.Add(x); }\n"
                "}\n"
                "public class AppDb { public DbSet<Order> Orders { get; set; } }\n",
                encoding="utf-8",
            )

            # 3. PHP / Laravel
            (root / "php").mkdir()
            (root / "php" / "routes.php").write_text(
                "<?php\n"
                "Route::post('/api/payroll/run', function() {\n"
                "  // SELECT * FROM payroll_batches\n"
                "});\n"
                "Schema::create('payroll_batches', function($t) {});\n",
                encoding="utf-8",
            )

            # 4. Rust / Axum + SeaORM
            (root / "rust").mkdir()
            (root / "rust" / "main.rs").write_text(
                '#[sea_orm(table_name = "ledger_entries")]\n'
                "struct Model {}\n"
                'fn router() { Router::new().route("/api/ledger", get(list_ledger)); }\n',
                encoding="utf-8",
            )

            # 5. Ruby / Rails
            (root / "ruby").mkdir()
            (root / "ruby" / "routes.rb").write_text(
                "get '/api/reports', to: 'reports#index'\n"
                "create_table :audit_logs do |t|\nend\n",
                encoding="utf-8",
            )

            # 6. Kotlin / Ktor
            (root / "kotlin").mkdir()
            (root / "kotlin" / "App.kt").write_text(
                'fun routing() {\n  post("/api/claims/submit") {\n  }\n}\n',
                encoding="utf-8",
            )

            # 7. C++ / Crow
            (root / "cpp").mkdir()
            (root / "cpp" / "server.cpp").write_text(
                'int main() {\n  CROW_ROUTE(app, "/api/metrics")([](){ return "ok"; });\n}\n',
                encoding="utf-8",
            )

            prop = extract_polyglot_proposal(root)
            triggers = {f["trigger"] for f in prop["flows"]}
            tables = {t["name"] for t in prop["tables"]}

            self.assertIn("POST /api/invoices/approve", triggers)
            self.assertIn("GET /api/health", triggers)
            self.assertIn("POST /api/orders/create", triggers)
            self.assertIn("POST /api/payroll/run", triggers)
            self.assertIn("GET /api/ledger", triggers)
            self.assertIn("GET /api/reports", triggers)
            self.assertIn("POST /api/claims/submit", triggers)
            self.assertIn("HTTP /api/metrics", triggers)

            for tbl in ("Invoice", "Orders", "payroll_batches", "ledger_entries", "audit_logs"):
                self.assertIn(tbl, tables)

            # End-to-end validation through real tmh.graph.build
            res = build_polyglot_structure(root)
            self.assertTrue(res["applied"], f"tmh validation errors: {res.get('errors')}")
            self.assertEqual(res["errors"], [])
            from tmh.graph import Graph
            g = Graph(root)
            self.assertGreaterEqual(len(g.nodes("flow")), 8)
            self.assertGreaterEqual(len(g.nodes("tbl")), 5)


if __name__ == "__main__":
    unittest.main()
