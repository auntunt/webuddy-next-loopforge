import React, { useCallback, useEffect, useState } from "react";
import { api } from "./api";

export default function WebuddyActionCard(props) {
  if (props?.spec?.type === "live_preview") {
    return <LivePreviewCard {...props} />;
  }
  if (props?.spec?.type === "kb_update") {
    return <KBUpdateCard {...props} />;
  }
  if (props?.spec?.type === "upload_archive") {
    return <UploadArchiveCard {...props} />;
  }
  return <EnvConfirmCard {...props} />;
}

function LivePreviewCard({ spec = null, projectId = "", autoDetected = false }) {
  const targetProjectId = (spec && spec.projectId) || projectId || "";
  const [status, setStatus] = useState(null);
  const [loading, setLoading] = useState(true);
  const [starting, setStarting] = useState(false);
  const [stopping, setStopping] = useState(false);
  const [error, setError] = useState("");
  const [activePath, setActivePath] = useState("/");
  const [expanded, setExpanded] = useState(true);
  const [showLogs, setShowLogs] = useState(false);
  const [iframeKey, setIframeKey] = useState(0);

  const fetchStatus = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const q = targetProjectId
        ? `?projectId=${encodeURIComponent(targetProjectId)}`
        : "";
      const res = await api(`/api/webuddy/preview/status${q}`);
      setStatus(res);
    } catch (err) {
      setError(err?.message || "获取预览状态失败");
    } finally {
      setLoading(false);
    }
  }, [targetProjectId]);

  useEffect(() => {
    fetchStatus();
  }, [fetchStatus]);

  useEffect(() => {
    const onUpdated = (e) => {
      const detail = e?.detail;
      if (!detail) return;
      const myPid = status?.projectId || targetProjectId;
      if (!myPid || !detail.projectId || myPid === detail.projectId) {
        setStatus(detail);
        if (detail.running) {
          setIframeKey((k) => k + 1);
        }
      }
    };
    window.addEventListener("webuddy:preview-updated", onUpdated);
    return () => window.removeEventListener("webuddy:preview-updated", onUpdated);
  }, [status?.projectId, targetProjectId]);

  async function handleStart() {
    setStarting(true);
    setError("");
    try {
      const pid = status?.projectId || targetProjectId;
      const res = await api("/api/webuddy/preview/start", {
        method: "POST",
        body: JSON.stringify({ projectId: pid }),
      });
      setStatus(res);
      setExpanded(true);
      setIframeKey((k) => k + 1);
      window.dispatchEvent(new CustomEvent("webuddy:preview-updated", { detail: res }));
    } catch (err) {
      setError(err?.message || "启动预览服务失败");
      setShowLogs(true);
      fetchStatus();
    } finally {
      setStarting(false);
    }
  }

  async function handleStop() {
    setStopping(true);
    setError("");
    try {
      const pid = status?.projectId || targetProjectId;
      const res = await api("/api/webuddy/preview/stop", {
        method: "POST",
        body: JSON.stringify({ projectId: pid }),
      });
      setStatus(res);
      window.dispatchEvent(new CustomEvent("webuddy:preview-updated", { detail: res }));
    } catch (err) {
      setError(err?.message || "停止预览服务失败");
    } finally {
      setStopping(false);
    }
  }

  const running = Boolean(status?.running && status?.previewUrl);
  const basePreviewUrl = (status?.previewUrl || "").replace(/\/+$/, "");
  const cleanActivePath = activePath.startsWith("/") ? activePath : `/${activePath}`;
  const fullPreviewUrl = running ? `${basePreviewUrl}${cleanActivePath}` : "";
  const quickPaths =
    Array.isArray(status?.quickPaths) && status.quickPaths.length > 0
      ? status.quickPaths
      : ["/"];
  const logs = Array.isArray(status?.logs) ? status.logs : [];

  return (
    <div
      className="webuddy-action-card"
      style={{
        marginTop: 12,
        marginBottom: 8,
        padding: "14px 16px",
        borderRadius: 14,
        border: running
          ? "1px solid rgba(22, 163, 74, 0.38)"
          : "1px solid rgba(59, 130, 246, 0.3)",
        background: running
          ? "linear-gradient(180deg, rgba(240, 253, 244, 0.94), rgba(255, 255, 255, 0.98))"
          : "linear-gradient(180deg, rgba(239, 246, 255, 0.86), rgba(255, 255, 255, 0.98))",
        boxShadow: "0 2px 10px rgba(15, 23, 42, 0.05)",
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
          <span
            style={{
              width: 10,
              height: 10,
              borderRadius: "50%",
              background: running ? "#16a34a" : starting ? "#f59e0b" : "#3b82f6",
              boxShadow: running ? "0 0 0 3px rgba(22,163,74,0.18)" : "none",
              display: "inline-block",
              flexShrink: 0,
            }}
          />
          <strong style={{ fontSize: 13.5, color: "#0f172a" }}>
            {running
              ? "实时应用预览已打穿端口"
              : (spec && spec.title) || "一键启动项目并打穿 HTTPS 预览通道"}
          </strong>
          {status?.projectName ? (
            <span style={{ fontSize: 12, color: "#64748b" }}>
              · {status.projectName}
            </span>
          ) : null}
        </div>
        {running ? (
          <code
            style={{
              fontSize: 11,
              color: "#15803d",
              background: "#dcfce7",
              padding: "2px 8px",
              borderRadius: 999,
            }}
          >
            本地端口 {status.port} → {basePreviewUrl}/
          </code>
        ) : (
          <span style={{ fontSize: 11.5, color: "#64748b" }}>
            {loading ? "正在检查服务状态..." : "服务端直连反向代理 · 免本地起端口"}
          </span>
        )}
      </div>

      {!running ? (
        <div style={{ fontSize: 12.5, color: "#475569", marginBottom: 10, lineHeight: 1.55 }}>
          平台将直接在服务端运行当前项目代码（自动补齐依赖、启用开发免登预览并通过
          <code style={{ margin: "0 4px", fontSize: 11.5 }}>/app-preview/</code>
          安全打穿端口），启动后即可直接在下方看到真实可交互页面。
        </div>
      ) : null}

      {error ? (
        <div
          style={{
            marginBottom: 10,
            padding: "8px 10px",
            borderRadius: 8,
            background: "#fef2f2",
            border: "1px solid #fecaca",
            color: "#b91c1c",
            fontSize: 12,
          }}
        >
          ❌ {error}
        </div>
      ) : null}

      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 8,
          flexWrap: "wrap",
          marginBottom: running && expanded ? 10 : 0,
        }}
      >
        {!running ? (
          <button
            type="button"
            disabled={starting || loading}
            onClick={handleStart}
            style={{
              appearance: "none",
              border: "none",
              borderRadius: 9,
              padding: "8px 15px",
              fontSize: 12.5,
              fontWeight: 600,
              cursor: starting ? "wait" : "pointer",
              background: starting ? "#93c5fd" : "#2563eb",
              color: "#ffffff",
              boxShadow: "0 1px 4px rgba(37, 99, 235, 0.28)",
            }}
          >
            {starting
              ? "⏳ 正在安装依赖并启动服务（首次约 15~30 秒）..."
              : "🚀 一键启动并打穿端口预览"}
          </button>
        ) : (
          <>
            {quickPaths.map((p) => {
              const isAct = cleanActivePath === p;
              return (
                <button
                  key={p}
                  type="button"
                  onClick={() => {
                    setActivePath(p);
                    setExpanded(true);
                  }}
                  style={{
                    appearance: "none",
                    border: isAct ? "1px solid #16a34a" : "1px solid #cbd5e1",
                    borderRadius: 8,
                    padding: "5px 10px",
                    fontSize: 12,
                    fontWeight: isAct ? 600 : 500,
                    cursor: "pointer",
                    background: isAct ? "#dcfce7" : "#ffffff",
                    color: isAct ? "#15803d" : "#334155",
                  }}
                >
                  {p === "/" ? "🏠 首页 (/)" : `📄 ${p}`}
                </button>
              );
            })}
            <button
              type="button"
              onClick={() => setIframeKey((k) => k + 1)}
              style={{
                appearance: "none",
                border: "1px solid #cbd5e1",
                borderRadius: 8,
                padding: "5px 10px",
                fontSize: 12,
                cursor: "pointer",
                background: "#ffffff",
                color: "#334155",
              }}
            >
              🔄 刷新
            </button>
            <a
              href={fullPreviewUrl}
              target="_blank"
              rel="noopener noreferrer"
              style={{
                display: "inline-flex",
                alignItems: "center",
                border: "1px solid #93c5fd",
                borderRadius: 8,
                padding: "5px 10px",
                fontSize: 12,
                fontWeight: 600,
                textDecoration: "none",
                background: "#eff6ff",
                color: "#1d4ed8",
              }}
            >
              🔗 新标签页打开
            </a>
            <button
              type="button"
              onClick={() => setExpanded((v) => !v)}
              style={{
                appearance: "none",
                border: "1px solid #cbd5e1",
                borderRadius: 8,
                padding: "5px 10px",
                fontSize: 12,
                cursor: "pointer",
                background: "#ffffff",
                color: "#334155",
              }}
            >
              {expanded ? "收起页面" : "展开页面"}
            </button>
            <button
              type="button"
              disabled={stopping}
              onClick={handleStop}
              style={{
                appearance: "none",
                border: "1px solid #fecaca",
                borderRadius: 8,
                padding: "5px 10px",
                fontSize: 12,
                cursor: stopping ? "wait" : "pointer",
                background: "#fef2f2",
                color: "#b91c1c",
              }}
            >
              {stopping ? "停止中..." : "⏹ 停止"}
            </button>
          </>
        )}
        {logs.length > 0 ? (
          <button
            type="button"
            onClick={() => setShowLogs((v) => !v)}
            style={{
              appearance: "none",
              border: "1px solid #e2e8f0",
              borderRadius: 8,
              padding: "5px 9px",
              fontSize: 11.5,
              cursor: "pointer",
              background: "#f8fafc",
              color: "#64748b",
            }}
          >
            {showLogs ? "隐藏日志" : `查看日志 (${logs.length})`}
          </button>
        ) : null}
      </div>

      {running && expanded ? (
        <div
          style={{
            borderRadius: 10,
            overflow: "hidden",
            border: "1px solid #cbd5e1",
            background: "#ffffff",
            boxShadow: "0 2px 8px rgba(15, 23, 42, 0.06)",
          }}
        >
          <div
            style={{
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
              padding: "6px 10px",
              background: "#f1f5f9",
              borderBottom: "1px solid #e2e8f0",
              fontSize: 11.5,
              color: "#475569",
            }}
          >
            <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
              🔒 {window.location.origin}
              {fullPreviewUrl}
            </span>
            <span style={{ flexShrink: 0, color: "#15803d", fontWeight: 600 }}>
              ● 实时连接
            </span>
          </div>
          <iframe
            key={iframeKey}
            src={fullPreviewUrl}
            title={`实时预览 - ${status?.projectName || targetProjectId}`}
            style={{
              width: "100%",
              height: 500,
              border: "none",
              display: "block",
              background: "#ffffff",
            }}
          />
        </div>
      ) : null}

      {showLogs && logs.length > 0 ? (
        <pre
          style={{
            marginTop: 10,
            marginBottom: 0,
            padding: "8px 10px",
            borderRadius: 8,
            background: "#0f172a",
            color: "#e2e8f0",
            fontSize: 11,
            lineHeight: 1.45,
            maxHeight: 180,
            overflow: "auto",
            whiteSpace: "pre-wrap",
          }}
        >
          {logs.join("\n")}
        </pre>
      ) : null}
    </div>
  );
}

function UploadArchiveCard({ spec = null, projectId = "", autoDetected = false }) {
  const targetProjectId = (spec && spec.projectId) || projectId || "";
  const [policy, setPolicy] = useState(null);
  const [loadingPolicy, setLoadingPolicy] = useState(true);
  const [uploading, setUploading] = useState(false);
  const [receipt, setReceipt] = useState(null);
  const [error, setError] = useState("");
  const inputRef = React.useRef(null);

  useEffect(() => {
    let active = true;
    api("/api/platform/policy")
      .then((res) => {
        if (active) setPolicy(res?.policy || { allowArchiveUpload: true, allowLooseProjects: true });
      })
      .catch(() => {
        if (active) setPolicy({ allowArchiveUpload: true, allowLooseProjects: true });
      })
      .finally(() => {
        if (active) setLoadingPolicy(false);
      });
    return () => {
      active = false;
    };
  }, []);

  async function handleFile(file) {
    if (!file) return;
    if (!targetProjectId) {
      setError("请先在右上角选择目标项目后再上传代码压缩包");
      return;
    }
    setUploading(true);
    setError("");
    try {
      const form = new FormData();
      form.append("file", file);
      const res = await fetch(
        `/api/webuddy/projects/${encodeURIComponent(targetProjectId)}/upload-archive`,
        {
          method: "POST",
          body: form,
          credentials: "same-origin",
        },
      );
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        throw new Error(data?.error || `上传失败 (HTTP ${res.status})`);
      }
      setReceipt(data);
    } catch (err) {
      setError(err?.message || "上传失败");
    } finally {
      setUploading(false);
      if (inputRef.current) inputRef.current.value = "";
    }
  }

  const allowed = policy ? policy.allowArchiveUpload !== false : true;
  const done = Boolean(receipt?.ok);

  return (
    <div
      style={{
        marginTop: 12,
        marginBottom: 8,
        borderRadius: 14,
        border: `1px solid ${done ? "#86efac" : allowed ? "#c7d2fe" : "#e2e8f0"}`,
        background: done
          ? "linear-gradient(180deg, #f0fdf4 0%, #ffffff 100%)"
          : allowed
            ? "linear-gradient(180deg, #f5f7ff 0%, #ffffff 100%)"
            : "#f8fafc",
        padding: "14px 16px",
        boxShadow: "0 2px 10px rgba(15, 23, 42, 0.05)",
        fontSize: 13,
        lineHeight: 1.5,
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 10,
          marginBottom: 10,
          flexWrap: "wrap",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 8, fontWeight: 600, color: "#0f172a" }}>
          <span style={{ fontSize: 15 }}>{done ? "🟢" : "📦"}</span>
          <span>
            {done
              ? "代码压缩包已导入并实时核验生效"
              : (spec && spec.title) ||
                (autoDetected
                  ? "支持直接上传代码压缩包（免 Git 快速接入）"
                  : "上传代码压缩包并自动构建知识库")}
          </span>
          {targetProjectId ? (
            <span style={{ fontWeight: 500, color: "#64748b", fontSize: 12 }}>
              · {receipt?.projectName || targetProjectId}
            </span>
          ) : null}
        </div>
        <span
          style={{
            fontSize: 11,
            fontWeight: 600,
            padding: "2px 8px",
            borderRadius: 999,
            background: done ? "#dcfce7" : allowed ? "#e0e7ff" : "#f1f5f9",
            color: done ? "#15803d" : allowed ? "#4338ca" : "#64748b",
          }}
        >
          {done ? "已核验生效" : allowed ? "松散模式已开启" : "管理员已禁用压缩包"}
        </span>
      </div>

      {loadingPolicy ? (
        <div style={{ color: "#64748b", padding: "4px 0" }}>正在核验平台治理策略...</div>
      ) : !allowed ? (
        <div style={{ color: "#475569", fontSize: 12.5 }}>
          🔒 平台管理员当前已关闭「压缩包直接导入」（严谨模式）。请通过标准 Git 仓库接入，或由管理员在「平台管理 → 项目治理策略」中开启松散项目管理。
        </div>
      ) : done ? (
        <div style={{ display: "grid", gap: 6 }}>
          <div
            style={{
              padding: "8px 10px",
              borderRadius: 9,
              background: "rgba(255,255,255,0.85)",
              border: "1px solid rgba(134,239,172,0.5)",
              fontSize: 12.5,
              color: "#166534",
            }}
          >
            ✅ 已解压 <strong>{receipt.fileCount}</strong> 个文件（{receipt.archiveName}），本地仓初始化完成
            {receipt.gitHead ? (
              <code style={{ marginLeft: 6, background: "#dcfce7", padding: "1px 6px", borderRadius: 4, fontSize: 11 }}>
                HEAD {receipt.gitHead}
              </code>
            ) : null}
          </div>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 6, fontSize: 11.5 }}>
            <span style={{ background: "#dcfce7", color: "#15803d", padding: "2px 8px", borderRadius: 6 }}>
              {receipt.kbBuilt
                ? receipt.flowCount > 0 || receipt.tableCount > 0
                  ? `✅ 知识库代码图谱已生成（${receipt.flowCount || 0} 条流程 · ${receipt.tableCount || 0} 张表）`
                  : `✅ 源码索引与知识库已就绪（共收录 ${receipt.fileCount || 0} 个文件，可直接问答与代码审查）`
                : "⚠️ 知识库待扫描"}
            </span>
            {receipt.sandboxId ? (
              <code style={{ background: "#e0f2fe", color: "#0369a1", padding: "2px 8px", borderRadius: 6 }}>
                沙箱就绪: {receipt.sandboxId}
              </code>
            ) : null}
          </div>
          <div style={{ fontSize: 11, color: "#64748b", marginTop: 2 }}>
            现在就可以直接在下方继续提问代码逻辑、接口接入或点击更新知识库 · 核验时间：{new Date(receipt.verifiedAt).toLocaleTimeString()}
          </div>
        </div>
      ) : (
        <div>
          <div style={{ fontSize: 12, color: "#475569", marginBottom: 10 }}>
            上传 <code>.zip</code> / <code>.tar.gz</code> 代码包后，平台会自动解压、建立本地 Git 溯源版本、配置轻量沙箱并构建代码知识库，无需先推到远程 Git。
          </div>
          <input
            ref={inputRef}
            type="file"
            accept=".zip,.tar.gz,.tgz"
            style={{ display: "none" }}
            onChange={(e) => handleFile(e.target.files?.[0])}
          />
          <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
            <button
              type="button"
              disabled={uploading}
              onClick={() => inputRef.current?.click()}
              style={{
                appearance: "none",
                border: "none",
                borderRadius: 9,
                padding: "7px 14px",
                fontSize: 12.5,
                fontWeight: 600,
                cursor: uploading ? "wait" : "pointer",
                background: uploading ? "#93c5fd" : "#4f46e5",
                color: "#ffffff",
              }}
            >
              {uploading ? "正在上传解压并构建代码图谱..." : "📦 选择代码压缩包上传 (.zip / .tar.gz)"}
            </button>
            <span style={{ fontSize: 11.5, color: "#64748b" }}>最大支持 100 MB</span>
          </div>
        </div>
      )}

      {error ? (
        <div
          style={{
            marginTop: 8,
            padding: "7px 10px",
            borderRadius: 8,
            background: "#fef2f2",
            color: "#b91c1c",
            fontSize: 12,
          }}
        >
          {error}
        </div>
      ) : null}
    </div>
  );
}

function KBUpdateCard({ spec = null, projectId = "", autoDetected = false }) {
  const targetProjectId = (spec && spec.projectId) || projectId || "";
  const [status, setStatus] = useState(null);
  const [loading, setLoading] = useState(true);
  const [updating, setUpdating] = useState(false);
  const [batchPages, setBatchPages] = useState([]);
  const [batchModel, setBatchModel] = useState("");
  const [batchDelta, setBatchDelta] = useState(0);
  const [error, setError] = useState("");

  const fetchStatus = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const q = targetProjectId
        ? `?projectId=${encodeURIComponent(targetProjectId)}`
        : "";
      const data = await api(`/api/webuddy/kb/status${q}`);
      setStatus(data);
    } catch (err) {
      setError(err?.message || "知识库状态核验失败");
    } finally {
      setLoading(false);
    }
  }, [targetProjectId]);

  useEffect(() => {
    fetchStatus();
  }, [fetchStatus]);

  useEffect(() => {
    const onKBUpdated = (e) => {
      const detail = e?.detail;
      if (!detail || !detail.status) return;
      const myPid = status?.projectId || targetProjectId;
      if (!myPid || !detail.projectId || myPid === detail.projectId) {
        setStatus(detail.status);
        if (Array.isArray(detail.updatedPages) && detail.updatedPages.length) {
          setBatchPages(detail.updatedPages);
        }
        if (detail.model) setBatchModel(detail.model);
      }
    };
    window.addEventListener("webuddy:kb-updated", onKBUpdated);
    return () => window.removeEventListener("webuddy:kb-updated", onKBUpdated);
  }, [status?.projectId, targetProjectId]);

  const [bgNotice, setBgNotice] = useState("");

  async function handleBackgroundUpdate() {
    setError("");
    setBgNotice("");
    try {
      const pid = status?.projectId || targetProjectId;
      const res = await api("/api/webuddy/kb/batch-update", {
        method: "POST",
        body: JSON.stringify({
          projectId: pid,
          limit: 3,
          query: (spec && spec.query) || "",
          async: true,
          all: true,
        }),
      });
      if (res?.ok) {
        setBgNotice("已启动后台全量持续分析（右下角浮动任务胶囊实时显示进度），完成后自动通知，你可以自由切换做其他工作！");
        window.dispatchEvent(new CustomEvent("webuddy:tasks-changed"));
      }
    } catch (err) {
      setError(err?.message || "启动后台更新失败");
    }
  }

  async function handleBatchUpdate() {
    setUpdating(true);
    setError("");
    try {
      const pid = status?.projectId || targetProjectId;
      const res = await api("/api/webuddy/kb/batch-update", {
        method: "POST",
        body: JSON.stringify({
          projectId: pid,
          limit: 2,
          query: (spec && spec.query) || "",
        }),
      });
      const updatedList = Array.isArray(res?.updatedPages) ? res.updatedPages : [];
      if (updatedList.length > 0) {
        setBatchPages(updatedList);
        setBatchDelta((prev) => prev + updatedList.length);
      }
      if (res?.model) setBatchModel(res.model);
      const nextStatus = {
        ok: true,
        projectId: res?.projectId || pid,
        projectName: res?.projectName || status?.projectName || pid,
        repoPath: res?.repoPath || status?.repoPath || "",
        gitHead: res?.gitHead || status?.gitHead || "",
        coverage: res?.coverageAfter || status?.coverage || {},
        recentUpdated: res?.recentUpdated || status?.recentUpdated || [],
        verifiedAt: res?.verifiedAt || new Date().toISOString(),
      };
      setStatus(nextStatus);
      window.dispatchEvent(
        new CustomEvent("webuddy:kb-updated", {
          detail: {
            projectId: nextStatus.projectId,
            status: nextStatus,
            updatedPages: updatedList,
            model: res?.model || "",
          },
        }),
      );
      if (updatedList.length === 0 && Array.isArray(res?.failedPages) && res.failedPages.length > 0) {
        const firstErr = res.failedPages[0]?.errors?.[0] || "源码溯源校验未通过";
        setError(`本次更新未写入：${firstErr}`);
      }
    } catch (err) {
      setError(err?.message || "增量更新失败");
    } finally {
      setUpdating(false);
    }
  }

  const cov = status?.coverage || {};
  const total = Number(cov.total || 0);
  const confirmed = Number(cov.confirmed || 0);
  const draft = Number(cov.draft || 0);
  const analyzed = Number(cov.analyzed || confirmed + draft);
  const pending = Number(cov.pending || 0);
  const stale = Number(cov.stale || 0);
  const remaining = pending + stale;
  const pct = total > 0 ? Math.min(100, Math.round((analyzed / total) * 100)) : 0;
  const confirmedPct = total > 0 ? Math.min(100, Math.round((confirmed / total) * 100)) : 0;
  const draftPct = Math.max(0, pct - confirmedPct);

  const displayPages =
    batchPages.length > 0
      ? batchPages
      : Array.isArray(status?.recentUpdated)
        ? status.recentUpdated.slice(0, 4)
        : [];

  const title =
    batchPages.length > 0
      ? `知识库已实时增量更新（+${batchPages.length} 页 · 源码溯源通过）`
      : error
        ? "知识库实时覆盖率与增量更新"
        : (spec && spec.title) ||
          (autoDetected
            ? "服务端知识库可信增量更新（无需沙箱 git clone）"
            : "知识库实时覆盖率与源码溯源核验");

  return (
    <div
      style={{
        marginTop: 12,
        marginBottom: 8,
        borderRadius: 14,
        border: `1px solid ${batchPages.length > 0 || remaining === 0 ? "#86efac" : "#c7d2fe"}`,
        background:
          batchPages.length > 0 || remaining === 0
            ? "linear-gradient(180deg, #f0fdf4 0%, #ffffff 100%)"
            : "linear-gradient(180deg, #f5f7ff 0%, #ffffff 100%)",
        padding: "14px 16px",
        boxShadow: "0 2px 10px rgba(15, 23, 42, 0.05)",
        fontSize: 13,
        lineHeight: 1.5,
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 10,
          marginBottom: 10,
          flexWrap: "wrap",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 8, fontWeight: 600, color: "#0f172a" }}>
          <span style={{ fontSize: 15 }}>📚</span>
          <span>{title}</span>
          {status?.projectName ? (
            <span style={{ fontWeight: 500, color: "#64748b", fontSize: 12 }}>
              · {status.projectName}
            </span>
          ) : null}
        </div>
        <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
          {status?.gitHead ? (
            <code
              style={{
                fontSize: 11,
                color: "#334155",
                background: "#f1f5f9",
                padding: "2px 7px",
                borderRadius: 6,
              }}
            >
              HEAD {status.gitHead}
            </code>
          ) : null}
          <span
            style={{
              fontSize: 11,
              fontWeight: 600,
              padding: "2px 8px",
              borderRadius: 999,
              background: "#dcfce7",
              color: "#15803d",
            }}
          >
            源码溯源已启用
          </span>
        </div>
      </div>

      {loading && !status ? (
        <div style={{ color: "#64748b", padding: "6px 0" }}>正在向服务端实时核验知识库覆盖率...</div>
      ) : (
        <>
          <div
            style={{
              padding: "10px 12px",
              borderRadius: 10,
              background: "rgba(255,255,255,0.85)",
              border: "1px solid rgba(148,163,184,0.22)",
              marginBottom: 10,
            }}
          >
            <div
              style={{
                display: "flex",
                justifyContent: "space-between",
                alignItems: "center",
                gap: 8,
                marginBottom: 6,
                fontSize: 12,
                color: "#334155",
              }}
            >
              <span>
                <strong>已分析 {analyzed}</strong> / 共 {total} 页（{pct}%）
                {batchDelta > 0 ? (
                  <span style={{ color: "#15803d", fontWeight: 600, marginLeft: 6 }}>
                    本次新增 +{batchDelta} 页
                  </span>
                ) : null}
              </span>
              <span style={{ color: "#64748b" }}>
                已确认 {confirmed} · 待确认 {draft} · 未分析 {pending}
                {stale > 0 ? ` · 过期 ${stale}` : ""}
              </span>
            </div>
            <div
              style={{
                height: 8,
                borderRadius: 999,
                background: "#e2e8f0",
                overflow: "hidden",
                display: "flex",
              }}
            >
              {confirmedPct > 0 ? (
                <div style={{ width: `${confirmedPct}%`, background: "#16a34a", transition: "width 0.3s" }} />
              ) : null}
              {draftPct > 0 ? (
                <div style={{ width: `${draftPct}%`, background: "#2563eb", transition: "width 0.3s" }} />
              ) : null}
            </div>
          </div>

          {displayPages.length > 0 ? (
            <div style={{ display: "grid", gap: 6, marginBottom: 10 }}>
              <div style={{ fontSize: 11, fontWeight: 600, color: "#475569" }}>
                {batchPages.length > 0 ? "本次实时更新与源码溯源回执" : "最近可信溯源更新记录"}
              </div>
              {displayPages.map((pg) => (
                <div
                  key={pg.pageId}
                  style={{
                    padding: "8px 10px",
                    borderRadius: 9,
                    background: "rgba(255,255,255,0.82)",
                    border: "1px solid rgba(148,163,184,0.22)",
                  }}
                >
                  <div style={{ display: "flex", justifyContent: "space-between", gap: 8, alignItems: "center" }}>
                    <span style={{ fontWeight: 600, color: "#0f172a", fontSize: 12.5 }}>
                      ✅ {pg.title || pg.pageId}
                    </span>
                    <div style={{ display: "flex", alignItems: "center", gap: 6, flexShrink: 0 }}>
                      {pg.commit ? (
                        <code style={{ fontSize: 10.5, color: "#475569", background: "#f1f5f9", padding: "1px 5px", borderRadius: 4 }}>
                          @{pg.commit}
                        </code>
                      ) : null}
                      <span style={{ fontSize: 11, color: "#64748b" }}>{pg.updated || ""}</span>
                    </div>
                  </div>
                  {pg.summary ? (
                    <div style={{ fontSize: 12, color: "#475569", marginTop: 3 }}>{pg.summary}</div>
                  ) : null}
                  <div style={{ display: "flex", flexWrap: "wrap", gap: 5, marginTop: 5 }}>
                    {pg.relPath ? (
                      <code style={{ fontSize: 10.5, color: "#0369a1", background: "#e0f2fe", padding: "1px 6px", borderRadius: 4 }}>
                        {pg.relPath}
                      </code>
                    ) : null}
                    {Array.isArray(pg.evidenceRefs) &&
                      pg.evidenceRefs.slice(0, 3).map((ref) => (
                        <code
                          key={ref}
                          style={{
                            fontSize: 10.5,
                            color: "#15803d",
                            background: "#dcfce7",
                            padding: "1px 6px",
                            borderRadius: 4,
                          }}
                        >
                          证据 {ref}
                        </code>
                      ))}
                  </div>
                </div>
              ))}
            </div>
          ) : null}
        </>
      )}

      {error ? (
        <div
          style={{
            marginBottom: 10,
            padding: "7px 10px",
            borderRadius: 8,
            background: "#fef2f2",
            color: "#b91c1c",
            fontSize: 12,
          }}
        >
          {error}
        </div>
      ) : null}

      {bgNotice ? (
        <div
          style={{
            marginBottom: 10,
            padding: "7px 10px",
            borderRadius: 8,
            background: "#eef2ff",
            color: "#3730a3",
            fontSize: 12,
          }}
        >
          🚀 {bgNotice}
        </div>
      ) : null}

      <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
        {remaining > 0 ? (
          <>
            <button
              type="button"
              disabled={updating}
              onClick={handleBackgroundUpdate}
              style={{
                appearance: "none",
                border: "none",
                borderRadius: 9,
                padding: "7px 14px",
                fontSize: 12.5,
                fontWeight: 600,
                cursor: "pointer",
                background: "#4f46e5",
                color: "#ffffff",
                boxShadow: "0 1px 3px rgba(79, 70, 229, 0.28)",
              }}
            >
              🚀 后台全部分析（剩余 {remaining} 页 · 不阻塞）
            </button>
            <button
              type="button"
              disabled={updating}
              onClick={handleBatchUpdate}
              style={{
                appearance: "none",
                border: "1px solid #bfdbfe",
                borderRadius: 9,
                padding: "6px 12px",
                fontSize: 12,
                fontWeight: 600,
                cursor: updating ? "wait" : "pointer",
                background: updating ? "#dbeafe" : "#eff6ff",
                color: "#1d4ed8",
              }}
            >
              {updating ? "正在分析源码并做溯源校验..." : "⚡ 立即更新 2 页"}
            </button>
          </>
        ) : null}
        <button
          type="button"
          disabled={loading || updating}
          onClick={fetchStatus}
          style={{
            appearance: "none",
            border: "1px solid #cbd5e1",
            borderRadius: 9,
            padding: "6px 12px",
            fontSize: 12,
            fontWeight: 500,
            cursor: loading ? "wait" : "pointer",
            background: "#ffffff",
            color: "#334155",
          }}
        >
          🔄 刷新实时覆盖率
        </button>
        {status?.verifiedAt ? (
          <span style={{ fontSize: 11, color: "#64748b", marginLeft: 4 }}>
            服务端实时核验：{new Date(status.verifiedAt).toLocaleTimeString()}
            {batchModel ? ` · ${batchModel}` : ""}
          </span>
        ) : null}
      </div>
    </div>
  );
}

function EnvConfirmCard({
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

  useEffect(() => {
    const onEnvUpdated = (e) => {
      const detail = e?.detail;
      if (!detail || !detail.readiness) return;
      const myPid = readiness?.projectId || targetProjectId;
      if (!myPid || !detail.projectId || myPid === detail.projectId) {
        setReadiness(detail.readiness);
        if (Array.isArray(detail.actions) && detail.actions.length > 0) {
          setActions(detail.actions);
        }
      }
    };
    window.addEventListener("webuddy:env-updated", onEnvUpdated);
    return () => window.removeEventListener("webuddy:env-updated", onEnvUpdated);
  }, [readiness?.projectId, targetProjectId]);

  async function handleAutoConfigure() {
    setConfiguring(true);
    setError("");
    try {
      const pid = readiness?.projectId || targetProjectId;
      const res = await api("/api/webuddy/env/auto-configure", {
        method: "POST",
        body: JSON.stringify({
          projectId: pid,
        }),
      });
      if (Array.isArray(res?.actions)) setActions(res.actions);
      if (res?.readiness) {
        setReadiness(res.readiness);
        window.dispatchEvent(
          new CustomEvent("webuddy:env-updated", {
            detail: {
              projectId: res.readiness.projectId || pid,
              readiness: res.readiness,
              actions: res.actions || [],
            },
          }),
        );
      } else {
        await fetchReadiness();
      }
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
