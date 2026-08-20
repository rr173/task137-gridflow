"use strict";
// GridFlow frontend — calls the real business API. No build step.

const api = (path, method, body) =>
  fetch(path, {
    method: method || "GET",
    headers: body ? { "Content-Type": "application/json" } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  }).then(async (r) => {
    const txt = await r.text();
    let data = txt ? JSON.parse(txt) : {};
    data._status = r.status;
    return data;
  });

const $ = (id) => document.getElementById(id);
const val = (id) => $(id).value;
const num = (id) => parseFloat($(id).value);
const show = (id, v) => {
  const el = $(id);
  el.textContent = typeof v === "string" ? v : JSON.stringify(v, null, 2);
};

function fmtVerdict(v) {
  if (v === "feasible") return '<span class="verdict-feasible">FEASIBLE</span>';
  if (v === "rejected") return '<span class="verdict-rejected">REJECTED</span>';
  return v || "";
}

async function refreshSummary() {
  try {
    const s = await api("/summary");
    let html = `母线: ${s.bus_count}  支路: ${s.branch_count}  机组: ${s.generator_count}  当前周期: ${s.current_period}  Slack: ${s.slack_bus || "—"}`;
    show("summary-out", html);
  } catch (e) {
    show("summary-out", "错误: " + e);
  }
}

async function refreshBuses() {
  const r = await api("/buses");
  show("bus-list", r.buses || []);
}
async function refreshBranches() {
  const r = await api("/branches");
  show("br-list", r.branches || []);
}
async function refreshGens() {
  const r = await api("/generators");
  show("gen-list", r.generators || []);
}

window.addEventListener("DOMContentLoaded", () => {
  $("refresh-summary").onclick = refreshSummary;
  refreshSummary();
  refreshBuses();
  refreshBranches();
  refreshGens();

  $("add-bus").onclick = async () => {
    const body = {
      id: val("bus-id"), name: val("bus-name"), type: val("bus-type"),
      vnom: num("bus-vnom"), vmin: num("bus-vmin"), vmax: num("bus-vmax"),
      vspec: num("bus-vspec"),
    };
    const r = await api("/buses", "POST", body);
    if (r._status >= 400) return alert("建母线失败: " + r.error);
    refreshBuses();
    refreshSummary();
  };

  $("add-br").onclick = async () => {
    const body = {
      id: val("br-id"), from_bus: val("br-from"), to_bus: val("br-to"),
      kind: val("br-kind"), r: num("br-r"), x: num("br-x"),
      mva_limit: num("br-mva"), tap: num("br-tap"),
    };
    const r = await api("/branches", "POST", body);
    if (r._status >= 400) return alert("建支路失败: " + r.error);
    refreshBranches();
    refreshSummary();
  };

  $("add-gen").onclick = async () => {
    const body = {
      id: val("gen-id"), bus_id: val("gen-bus"),
      pmin: num("gen-pmin"), pmax: num("gen-pmax"), ramp_mw: num("gen-ramp"),
      min_up: parseInt(val("gen-minup"), 10), min_down: parseInt(val("gen-mindown"), 10),
      qmin: num("gen-qmin"), qmax: num("gen-qmax"), status: val("gen-status"),
    };
    const r = await api("/generators", "POST", body);
    if (r._status >= 400) return alert("建发电机失败: " + r.error);
    refreshGens();
    refreshSummary();
  };

  $("add-load").onclick = async () => {
    const body = {
      bus_id: val("load-bus"), period: parseInt(val("load-period"), 10),
      p_mw: num("load-p"), q_mvar: num("load-q"),
    };
    const r = await api("/loads", "POST", body);
    if (r._status >= 400) return alert("录入负荷失败: " + r.error);
  };

  $("seed-period").onclick = async () => {
    const r = await api("/periods", "POST", { reserve_req: num("seed-reserve") });
    if (r._status >= 400) return alert("初始化周期失败: " + r.error);
    refreshSummary();
  };

  $("advance").onclick = async () => {
    const r = await api("/periods/advance", "POST");
    if (r._status >= 400) return alert("推进周期失败: " + r.error);
    refreshSummary();
  };

  $("run-dispatch").onclick = async () => {
    const r = await api("/dispatch/run", "POST", { period: parseInt(val("dispatch-period"), 10) });
    if (r._status >= 400) return alert("调度失败: " + r.error);
    renderDispatch(r);
    refreshSummary();
  };

  $("release").onclick = async () => {
    const r = await api("/dispatch/" + encodeURIComponent(val("dispatch-period")) + "/release", "POST");
    if (r._status >= 400) return alert("放行失败: " + r.error);
    refreshSummary();
  };

  $("reconcile").onclick = async () => {
    const r = await api("/reconcile", "POST");
    if (r._status >= 400) return alert("重算失败: " + r.error);
    show("result-out", r.message);
  };

  $("gen-status").onclick = async () => {
    const r = await api("/generators/" + encodeURIComponent(val("status-gen")) + "/status");
    if (r._status >= 400) return alert("查询失败: " + r.error);
    show("gen-status-out", r);
  };
  $("commit-gen").onclick = async () => {
    const r = await api("/generators/" + encodeURIComponent(val("status-gen")) + "/commit", "POST");
    if (r._status >= 400) return alert("投运失败: " + r.error);
    show("gen-status-out", r);
  };
  $("decommit-gen").onclick = async () => {
    const r = await api("/generators/" + encodeURIComponent(val("status-gen")) + "/decommit", "POST");
    if (r._status >= 400) return alert("停运失败: " + r.error);
    show("gen-status-out", r);
  };

  $("outage").onclick = async () => {
    const r = await api("/branches/" + encodeURIComponent(val("outage-id")) + "/outage", "POST");
    if (r._status >= 400) return alert("停运失败: " + r.error);
    refreshBranches();
  };
  $("restore").onclick = async () => {
    const r = await api("/branches/" + encodeURIComponent(val("outage-id")) + "/restore", "POST");
    if (r._status >= 400) return alert("复役失败: " + r.error);
    refreshBranches();
  };

  $("load-events").onclick = async () => {
    const r = await api("/events?limit=50");
    show("events-out", r.events || []);
  };
});

function renderDispatch(r) {
  let html = `<div>判定: ${fmtVerdict(r.verdict)}</div>`;
  html += `<div>总发电 ${fmt(r.total_gen)} MW  总负荷 ${fmt(r.total_load)} MW  网损 ${fmt(r.total_loss)} MW</div>`;
  html += "<h3>母线电压</h3>" + table(["母线", "类型", "|V| pu", "相角 rad"], (r.buses || []).map((b) => [b.id, b.type, fmt(b.vmag), fmt(b.theta)]));
  html += "<h3>线路潮流</h3>" + table(["支路", "from→to", "P MW", "Q Mvar", "S MVA", "负载率%", "越限"], (r.branches || []).map((b) => [b.id, b.from + "→" + b.to, fmt(b.p_from), fmt(b.q_from), fmt(b.s_mva), fmt(b.loading), b.overload ? "是" : "否"]));
  html += "<h3>机组出力</h3>" + table(["机组", "母线", "状态", "P MW", "Q Mvar"], (r.generators || []).map((g) => [g.id, g.bus_id, g.status, fmt(g.p_output), fmt(g.q_output)]));
  $("result-out").innerHTML = html;
  show("violations-out", r.violations || "（无越限）");
}

function table(headers, rows) {
  let h = headers.map((x) => `<th>${x}</th>`).join("");
  let b = rows.map((row) => "<tr>" + row.map((c, i) => `<td class="${i >= 2 ? "num" : ""}">${c}</td>`).join("") + "</tr>").join("");
  return `<table><thead><tr>${h}</tr></thead><tbody>${b}</tbody></table>`;
}
function fmt(x) {
  if (x === undefined || x === null) return "—";
  if (typeof x !== "number") return String(x);
  return Math.abs(x) >= 100 ? x.toFixed(1) : x.toFixed(3);
}
