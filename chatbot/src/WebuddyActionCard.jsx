import React, { useCallback, useEffect, useState } from "react";
import { api } from "./api";

export default function WebuddyActionCard({
  spec = null,
  projectId = "",
  autoDetected = false,
}) {
  const targetProjectId = (spec && spec.projectId) || projectId || "";
  const [readiness, setReadiness] = useState(null);
  const [loading, setLoading] = useState(true);
  const [configuring, setConfiguring] = useState(false);
  const [actions, setActions] = useState([]);
  const [error, setError] = useState("");

  const fetchReadiness = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const q = targetProjectId
        ? `?projectId=${encodeURIComponent(targetProjectId)}`
        : "";
      const data = await api(`/api/webuddy/env/project-readiness${q}`);
      setReadiness(data);
    } catch (err) {
      setError(err?.message || "状态核验失败");
    } finally {
      setLoading(false);
    }
  }, [targetProjectId]);

  useEffect(() => {
    fetchReadiness();
  }, [fetchReadiness]);

  async function handleAutoConfigure() {
    setConfiguring(true);
    setError("");
    try {
      const res = await api("/api/webuddy/env/auto-configure", {
        method: "POST",
        body: JSON.stringify({
          projectId: readiness?.projectId || targetProjectId,
        }),
      });
      if (Array.isArray(res?.actions)) setActions(res.actions);
      if (res?.readiness) setReadiness(res.readiness);
      else await fetchReadiness();
    } catch (err) {
      setError(err?.message || "自动配置失败");
    } finally {
      setConfiguring(false);
    }
  }

  const allGood = Boolean(readiness?.overallReady);
  const title =
    (spec && spec.title) ||
    (allGood
      ? "工作区环境已实时核验生效"
      : autoDetected
        ? "检测到缺少工作区沙箱 · 可直接一键配置"
        : "项目运行环境核验");

  const rows = readiness
    ? [
        {
          label: "工作区沙箱",
          ready: readiness.sandbox?.ready,
          detail: readiness.sandbox?.detail,
          id: readiness.sandbox?.id,
        },
        {
          label: "代码知识库",
          ready: readiness.kb?.ready,
          detail: readiness.kb?.detail,
        },
        {
          label: "在线执行机",
          ready: readiness.runner?.ready,
          detail: readiness.runner?.detail,
        },
      ]
    : [];

  return (
    <div
      style={{
        marginTop: 12,
        marginBottom: 8,
        borderRadius: 12,
        border: `1px solid ${allGood ? "rgba(16,185,129,0.45)" : "rgba(59,130,246,0.4)"}`,
        background: "var(--surface, #ffffff)",
        padding: "14px 16px",
        boxShadow: "0 2px 10px rgba(15,23,42,0.05)",
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 10,
          flexWrap: "wrap",
          marginBottom: 10,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <span style={{ fontSize: 16 }}>{allGood ? "🟢" : "⚡"}</span>
          <strong style={{ fontSize: 14 }}>
            {title}
            {readiness?.projectName ? ` · ${readiness.projectName}` : ""}
          </strong>
        </div>
        <span
          style={{
            fontSize: 11.5,
            padding: "2px 8px",
            borderRadius: 999,
            fontWeight: 600,
            background: allGood
              ? "rgba(16,185,129,0.12)"
              : "rgba(245,158,11,0.14)",
            color: allGood ? "#059669" : "#b45309",
          }}
        >
          {loading
            ? "实时核验中…"
            : allGood
              ? "已实时核验生效"
              : "待一键配置"}
        </span>
      </div>

      {rows.length > 0 ? (
        <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
          {rows.map((r) => (
            <div
              key={r.label}
              style={{
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                gap: 8,
                fontSize: 12.5,
                padding: "6px 10px",
                borderRadius: 8,
                background: "var(--surface-2, #f8fafc)",
              }}
            >
              <span>
                {r.ready ? "✅" : "⚠️"} <b>{r.label}</b>：{r.detail}
              </span>
              {r.id ? (
                <code style={{ fontSize: 11, opacity: 0.75 }}>{r.id}</code>
              ) : null}
            </div>
          ))}
        </div>
      ) : null}

      {actions.length > 0 ? (
        <div
          style={{
            marginTop: 8,
            padding: "8px 10px",
            borderRadius: 8,
            background: "rgba(16,185,129,0.08)",
            fontSize: 12.5,
            color: "#065f46",
          }}
        >
          {actions.map((a, i) => (
            <div key={i}>✓ {a}</div>
          ))}
        </div>
      ) : null}

      {error ? (
        <div
          style={{
            marginTop: 8,
            fontSize: 12,
            color: "#dc2626",
          }}
        >
          ❌ {error}
        </div>
      ) : null}

      <div
        style={{
          marginTop: 12,
          display: "flex",
          alignItems: "center",
          gap: 8,
          flexWrap: "wrap",
        }}
      >
        {!allGood ? (
          <button
            type="button"
            disabled={configuring || loading}
            onClick={handleAutoConfigure}
            style={{
              background: "#2563eb",
              color: "#fff",
              border: "none",
              borderRadius: 8,
              padding: "7px 14px",
              fontSize: 13,
              fontWeight: 600,
              cursor: configuring ? "wait" : "pointer",
            }}
          >
            {configuring ? "正在写入配置并核验…" : "⚡ 一键自动配置并核验"}
          </button>
        ) : null}
        <button
          type="button"
          disabled={loading || configuring}
          onClick={fetchReadiness}
          style={{
            background: "transparent",
            color: "var(--text, #334155)",
            border: "1px solid var(--border, #cbd5e1)",
            borderRadius: 8,
            padding: "6px 12px",
            fontSize: 12.5,
            cursor: "pointer",
          }}
        >
          🔄 重新核验实时状态
        </button>
        {readiness?.verifiedAt ? (
          <span style={{ fontSize: 11.5, color: "var(--muted, #64748b)" }}>
            服务端实时核验：
            {new Date(readiness.verifiedAt).toLocaleTimeString()}
          </span>
        ) : null}
      </div>
    </div>
  );
}
