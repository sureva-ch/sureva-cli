package changes

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"path/filepath"
	"sort"
)

type htmlNode struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Type    string `json:"type"`
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
}

type htmlGraph struct {
	Repository string            `json:"repository,omitempty"`
	Base       string            `json:"base"`
	Nodes      []htmlNode        `json:"nodes"`
	Edges      []Edge            `json:"edges"`
	Diffs      map[string]string `json:"diffs"`
	Release    string            `json:"release"`
	Changelog  []ChangelogGroup  `json:"changelog"`
	Checklist  []ChecklistItem   `json:"checklist"`
}

// WriteHTMLGraph writes a self-contained interactive HTML visualization for graph.
func WriteHTMLGraph(w io.Writer, graph Graph) error {
	payload, err := json.Marshal(htmlGraphData(graph))
	if err != nil {
		return fmt.Errorf("marshal graph data: %w", err)
	}
	data := struct {
		GraphBase64 string
	}{
		// URL-safe base64 avoids '+' and '/', which html/template would escape
		// to HTML entities inside the <script> raw-text block (breaking atob).
		GraphBase64: base64.URLEncoding.EncodeToString(payload),
	}
	return changesHTMLTemplate.Execute(w, data)
}

func htmlGraphData(graph Graph) htmlGraph {
	status := make(map[string]string, len(graph.Nodes))
	nodes := make(map[string]bool, len(graph.Nodes)+len(graph.Edges)*2)
	for _, node := range graph.Nodes {
		node = filepath.ToSlash(node)
		s := graph.Statuses[node]
		if s == "" {
			s = string(StatusModified)
		}
		status[node] = s
		nodes[node] = true
	}
	for _, edge := range graph.Edges {
		nodes[filepath.ToSlash(edge.From)] = true
		nodes[filepath.ToSlash(edge.To)] = true
	}
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		if id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	viewNodes := make([]htmlNode, 0, len(ids))
	for _, id := range ids {
		s, ok := status[id]
		if !ok {
			s = "context"
		}
		c := graph.Churn[id]
		viewNodes = append(viewNodes, htmlNode{ID: id, Status: s, Type: nodeChecklistType(id, s), Added: c.Added, Deleted: c.Deleted})
	}
	edges := make([]Edge, 0, len(graph.Edges))
	for _, edge := range graph.Edges {
		edges = append(edges, Edge{From: filepath.ToSlash(edge.From), To: filepath.ToSlash(edge.To)})
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From == edges[j].From {
			return edges[i].To < edges[j].To
		}
		return edges[i].From < edges[j].From
	})
	diffs := make(map[string]string, len(graph.Diffs))
	for path, diff := range graph.Diffs {
		diffs[filepath.ToSlash(path)] = diff
	}
	return htmlGraph{Repository: graph.Repository, Base: graph.Base, Nodes: viewNodes, Edges: edges, Diffs: diffs, Release: graph.Release, Changelog: graph.Changelog, Checklist: graph.Checklist}
}

func nodeChecklistType(path, status string) string {
	if status == "context" {
		return "context"
	}
	switch {
	case reChangelog.MatchString(path):
		return "changelog"
	case reTest.MatchString(path):
		return "tests"
	case reMigration.MatchString(path):
		return "migrations"
	case reDoc.MatchString(path):
		return "docs"
	case !reTest.MatchString(path) && !reMigration.MatchString(path) && reCode.MatchString(path):
		return "code"
	default:
		return "other"
	}
}

var changesHTMLTemplate = template.Must(template.New("changes-html").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>sureva changes graph</title>
<style>
:root{color-scheme:dark;--bg:#070a0f;--panel:#101722d9;--line:#71809688;--text:#e8f0ff;--muted:#91a0b8;--accent:#8ff0c5;--node:#1c2636;--node-stroke:#7d8da8;--new:#163d31;--new-stroke:#8ff0c5;--mod:#3a340f;--mod-stroke:#ffd166;--del:#3a1414;--del-stroke:#ff6b6b;--ren:#122d3a;--ren-stroke:#6bc1ff}*{box-sizing:border-box}html,body{margin:0;height:100%;overflow:hidden;background:radial-gradient(circle at 12% 18%,#17382d 0,#070a0f 28%,#03050a 100%);font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;color:var(--text)}body:before{content:"";position:fixed;inset:0;pointer-events:none;opacity:.22;background-image:linear-gradient(#ffffff08 1px,transparent 1px),linear-gradient(90deg,#ffffff08 1px,transparent 1px);background-size:34px 34px}.sidebar{position:fixed;top:0;left:0;bottom:0;z-index:2;width:300px;display:flex;flex-direction:column;padding:16px 14px;border-right:1px solid #ffffff1c;background:var(--panel);backdrop-filter:blur(14px);box-shadow:0 20px 80px #0008}.search{margin:12px 0 8px;padding:8px 10px;border:1px solid #ffffff1c;border-radius:10px;background:#0b111b;color:var(--text);font:inherit;font-size:13px;outline:none}.search:focus{border-color:var(--accent)}.index{list-style:none;margin:0;padding:0;overflow:auto;flex:1;display:flex;flex-direction:column;gap:1px}.index li{display:flex;align-items:center;gap:8px;padding:5px 8px;border-radius:8px;color:var(--muted);font-size:12px;cursor:pointer;white-space:nowrap;overflow:hidden}.index li:hover{background:#ffffff10;color:var(--text)}.index li.active{background:#ffffff1e;color:var(--text)}.index li span{flex:1;overflow:hidden;text-overflow:ellipsis}.ch{flex:none;font-style:normal;font-size:11px;font-variant-numeric:tabular-nums;white-space:nowrap}.ch .pl{color:#7fd79e}.ch .mi{color:#ff9b9b;margin-left:6px}.index[hidden]{display:none}.tabs{display:flex;gap:4px;margin:10px 0 6px}.tabs[hidden]{display:none}.tab{flex:1;padding:6px 8px;border:1px solid #ffffff1c;border-radius:8px;background:#0b111b;color:var(--muted);font:inherit;font-size:12px;cursor:pointer}.tab.active{background:#ffffff1e;color:var(--text);border-color:var(--accent)}.changelog{flex:1;overflow:auto;display:flex;flex-direction:column;gap:2px}.changelog[hidden]{display:none}.cl-group{margin:10px 0 4px;color:var(--accent);font-size:12px;font-weight:650}.cl-commit{display:flex;align-items:baseline;gap:6px;padding:3px 6px;border-radius:6px;font-size:12px;color:var(--text)}.cl-commit:hover{background:#ffffff0d}.cl-scope{color:#ffd166;font-weight:600}.cl-scope:empty{display:none}.cl-scope:not(:empty):after{content:":"}.cl-subj{flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.cl-hash{color:var(--muted);font-size:11px;font-variant-numeric:tabular-nums}.checklist{margin:12px 0 2px;display:flex;flex-direction:column;gap:4px}.check{display:flex;align-items:center;gap:8px;color:var(--muted);border:1px solid transparent;background:transparent;text-align:left;border-radius:8px;padding:3px 5px;font:inherit;font-size:12px;line-height:1.25;cursor:pointer}.check:hover{background:#ffffff0d}.check.ok{color:var(--text)}.check.active{border-color:var(--accent);background:#ffffff14}.check:not(.available){opacity:.45;cursor:default}.check .mark{flex:none;width:16px;height:16px;border-radius:5px;display:grid;place-items:center;font-size:11px;border:1px solid var(--node-stroke);color:transparent}.check.ok .mark{background:var(--new);border-color:var(--new-stroke);color:var(--new-stroke)}.diff{position:fixed;top:0;right:0;bottom:0;z-index:3;width:min(560px,52vw);display:flex;flex-direction:column;background:#0a0f17f2;border-left:1px solid #ffffff1c;backdrop-filter:blur(14px);box-shadow:-20px 0 80px #0009}.diff[hidden]{display:none}.diff header{display:flex;align-items:center;gap:10px;padding:14px 16px;border-bottom:1px solid #ffffff14}.diff header .p{flex:1;font-size:13px;color:var(--text);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.diff .close{cursor:pointer;border:1px solid #ffffff1c;background:#0b111b;color:var(--muted);border-radius:8px;padding:4px 10px;font:inherit;font-size:12px}.diff .close:hover{color:var(--text)}.diff .body{flex:1;overflow:auto;padding:8px 0;font-size:12px;line-height:1.5}.dl{padding:0 13px;border-left:3px solid transparent;white-space:pre-wrap;word-break:break-word;color:#cdd6e6}.dl.add{background:#0f2a1a;border-left-color:#3ddc84}.dl.del{background:#2e1414;border-left-color:#ff6b6b}.dl.hunk{background:#0d1c26;color:#6bc1ff;border-left-color:#2b4a5e}.dl.meta{color:#6b7a92}.t-kw{color:#c792ea}.t-str{color:#c3e88d}.t-com{color:#7c8aa0;font-style:italic}.t-num{color:#f78c6c}.eyebrow{margin:0 0 7px;color:var(--accent);letter-spacing:.16em;text-transform:uppercase;font-size:11px}.title{margin:0;font-size:19px;font-weight:750}.meta{margin:8px 0 0;color:var(--muted);font-size:12px;line-height:1.45}.legend{display:flex;gap:10px;flex-wrap:wrap;margin-top:12px;color:var(--muted);font-size:12px}.dot{display:inline-block;width:10px;height:10px;border-radius:99px;margin-right:6px;border:1px solid var(--node-stroke);background:var(--node)}.dot.new{border-color:var(--new-stroke);background:var(--new)}.dot.modified{border-color:var(--mod-stroke);background:var(--mod)}.dot.deleted{border-color:var(--del-stroke);background:var(--del)}.dot.renamed{border-color:var(--ren-stroke);background:var(--ren)}svg{position:fixed;inset:0;width:100%;height:100%;cursor:grab}.edge{stroke:var(--line);stroke-width:1.4;fill:none;stroke-linecap:round;opacity:.85}.node circle{fill:var(--node);stroke:var(--node-stroke);stroke-width:1.5;filter:drop-shadow(0 8px 18px #0008)}.node.context circle{opacity:.28}.node.new circle{fill:var(--new);stroke:var(--new-stroke);stroke-width:2.4;filter:drop-shadow(0 0 7px var(--new-stroke))}.node.modified circle{fill:var(--mod);stroke:var(--mod-stroke);stroke-width:2.4;filter:drop-shadow(0 0 7px var(--mod-stroke))}.node.deleted circle{fill:var(--del);stroke:var(--del-stroke);stroke-width:2.4;filter:drop-shadow(0 0 7px var(--del-stroke))}.node.renamed circle{fill:var(--ren);stroke:var(--ren-stroke);stroke-width:2.4;filter:drop-shadow(0 0 7px var(--ren-stroke))}.node text{fill:var(--text);font-size:13.5px;paint-order:stroke;stroke:#05070b;stroke-width:4.5px;stroke-linejoin:round;pointer-events:none}.node.context text{fill:var(--muted);opacity:.32;font-size:11px}.node.new text,.node.modified text,.node.deleted text,.node.renamed text{fill:#f2f8ff;font-weight:650}.empty{position:fixed;inset:0;display:grid;place-items:center;color:var(--muted);font-size:15px}.empty[hidden]{display:none}.node.selected circle{stroke:#fff;stroke-width:3.6;animation:sel 1s ease-out}@keyframes sel{from{stroke-width:8}to{stroke-width:3.6}}.tip{position:fixed;right:18px;bottom:18px;color:#91a0b899;font-size:12px}</style>
</head>
<body>
<aside class="sidebar"><p class="eyebrow">sureva changes</p><h1 class="title">Branch import graph</h1><p class="meta" id="meta"></p><div class="legend"><span><i class="dot new"></i>new</span><span><i class="dot modified"></i>edited</span><span><i class="dot deleted"></i>deleted</span><span><i class="dot renamed"></i>renamed</span><span><i class="dot"></i>context</span></div><div class="checklist" id="checklist"></div><div class="tabs" id="tabs" hidden><button class="tab active" data-tab="files" type="button">Files</button><button class="tab" data-tab="changelog" type="button">Changelog</button></div><input class="search" id="search" type="search" placeholder="Filter files…" autocomplete="off"><ul class="index" id="index"></ul><div class="changelog" id="changelog" hidden></div></aside>
<div class="empty" id="empty" hidden>No changed files found.</div><section class="diff" id="diff" hidden><header><span class="p" id="diff-title"></span><span class="ch" id="diff-churn"></span><button class="close" id="diff-close" type="button">✕ close</button></header><div class="body" id="diff-body"></div></section><div class="tip">Drag nodes · scroll to zoom · drag canvas to pan · double-click to fit</div>
<svg id="graph" role="img" aria-label="Interactive graph of branch changes and import edges"><g id="viewport"><g id="edges"></g><g id="nodes"></g></g></svg>
<script id="graph-data" type="application/octet-stream">{{.GraphBase64}}</script>
<script>
(() => {
  const graph = JSON.parse(atob(document.getElementById('graph-data').textContent.trim().replace(/-/g, '+').replace(/_/g, '/')));
  const svg = document.getElementById('graph');
  const viewport = document.getElementById('viewport');
  const edgesLayer = document.getElementById('edges');
  const nodesLayer = document.getElementById('nodes');
  const titleEl = document.querySelector('.title');
  if (graph.repository) { titleEl.textContent = graph.repository; document.title = graph.repository + ' changes'; }
  if (graph.release) {
    if (!graph.repository) titleEl.textContent = 'Release ' + graph.release;
    const changedCount = graph.nodes.filter(n => n.status !== 'context').length;
    const commitCount = (graph.changelog || []).reduce((n, g) => n + g.commits.length, 0);
    document.getElementById('meta').textContent = (graph.release ? 'release ' + graph.release + ' · ' : '') + 'since ' + (graph.base || 'start') + ' · ' + changedCount + ' files · ' + commitCount + ' commits';
  } else {
    document.getElementById('meta').textContent = 'base: ' + (graph.base || 'main') + ' · ' + graph.nodes.length + ' nodes · ' + graph.edges.length + ' edges';
  }
  const activeTypes = new Set();
  const checklistEl = document.getElementById('checklist');
  for (const item of (graph.checklist || [])) {
    const row = document.createElement('button');
    row.type = 'button';
    row.className = 'check' + (item.ok ? ' ok available' : '');
    row.dataset.type = item.key;
    row.setAttribute('aria-pressed', 'false');
    row.innerHTML = '<span class="mark">✓</span><span class="label"></span>';
    row.querySelector('.label').textContent = item.label;
    row.addEventListener('click', () => {
      if (!item.ok) return;
      if (activeTypes.has(item.key)) activeTypes.delete(item.key); else activeTypes.add(item.key);
      row.classList.toggle('active', activeTypes.has(item.key));
      row.setAttribute('aria-pressed', activeTypes.has(item.key) ? 'true' : 'false');
      applyFilters();
    });
    checklistEl.appendChild(row);
  }
  document.getElementById('empty').hidden = graph.nodes.length !== 0;
  const width = () => window.innerWidth;
  const height = () => window.innerHeight;
  const LEFT = 324; // sidebar width plus gap; graph is centered in the space to its right
  const nodeByID = new Map(graph.nodes.map((node, i) => {
    const angle = (i / Math.max(graph.nodes.length, 1)) * Math.PI * 2;
    const radius = 70 + Math.sqrt(Math.max(graph.nodes.length, 1)) * 14;
    return [node.id, {...node, x: width()/2 + Math.cos(angle)*radius, y: height()/2 + Math.sin(angle)*radius, vx: 0, vy: 0}];
  }));
  graph.edges.forEach(edge => {
    if (!nodeByID.has(edge.from)) nodeByID.set(edge.from, {id: edge.from, status: 'context', x: width()/2, y: height()/2, vx:0, vy:0});
    if (!nodeByID.has(edge.to)) nodeByID.set(edge.to, {id: edge.to, status: 'context', x: width()/2, y: height()/2, vx:0, vy:0});
  });
  const nodes = [...nodeByID.values()];
  const links = graph.edges.map(edge => ({source: nodeByID.get(edge.from), target: nodeByID.get(edge.to)}));
  const edgeEls = links.map(() => {
    const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
    path.setAttribute('class', 'edge');
    edgesLayer.appendChild(path);
    return path;
  });
  const nodeEls = nodes.map(node => {
    const group = document.createElementNS('http://www.w3.org/2000/svg', 'g');
    group.setAttribute('class', 'node ' + (node.status || 'context'));
    const r = node.status === 'context' ? 6 : 13;
    group.innerHTML = '<circle r="' + r + '"></circle><text x="' + (r + 4) + '" y="5"></text>';
    group.querySelector('text').textContent = node.id;
    nodesLayer.appendChild(group);
    group.addEventListener('pointerdown', event => { autoFit = false; drag.node = node; drag.id = event.pointerId; drag.moved = false; drag.sx = event.clientX; drag.sy = event.clientY; group.setPointerCapture(event.pointerId); event.stopPropagation(); });
    return group;
  });
  const elById = new Map(nodes.map((node, i) => [node.id, nodeEls[i]]));
  const liById = new Map();
  const visibleIds = new Set(nodes.map(node => node.id));
  function matchesType(node) {
    return node.status === 'context' || activeTypes.size === 0 || activeTypes.has(node.type || 'code');
  }
  function changedVisible(node) {
    return node.status !== 'context' && matchesType(node);
  }
  function recomputeVisibleIds() {
    visibleIds.clear();
    for (const node of nodes) if (changedVisible(node)) visibleIds.add(node.id);
    for (const {source, target} of links) {
      if (source.status !== 'context' && target.status === 'context' && visibleIds.has(source.id)) visibleIds.add(target.id);
      if (target.status !== 'context' && source.status === 'context' && visibleIds.has(target.id)) visibleIds.add(source.id);
    }
  }
  let transform = {x: 0, y: 0, k: 1};
  let pan = null;
  let autoFit = true;
  const drag = {node: null, id: null};
  const applyTransform = () => viewport.setAttribute('transform', 'translate(' + transform.x + ' ' + transform.y + ') scale(' + transform.k + ')');
  function fitView() {
    const fitNodes = nodes.filter(n => visibleIds.has(n.id));
    if (!fitNodes.length) return;
    let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
    for (const n of fitNodes) { minX = Math.min(minX, n.x); minY = Math.min(minY, n.y); maxX = Math.max(maxX, n.x); maxY = Math.max(maxY, n.y); }
    const padX = 34, padY = 30;
    const availW = width() - LEFT - padX * 2, availH = height() - padY * 2;
    const w = (maxX - minX) || 1, h = (maxY - minY) || 1;
    transform.k = Math.min(6.5, Math.max(.12, Math.min(availW / w, availH / h)));
    transform.x = (LEFT + (width() - LEFT) / 2) - (minX + maxX) / 2 * transform.k;
    transform.y = height() / 2 - (minY + maxY) / 2 * transform.k;
    applyTransform();
  }
  svg.addEventListener('pointerdown', event => { autoFit = false; pan = {x: event.clientX, y: event.clientY, ox: transform.x, oy: transform.y}; svg.setPointerCapture(event.pointerId); });
  svg.addEventListener('dblclick', () => { autoFit = true; fitView(); });
  window.addEventListener('resize', fitView);
  svg.addEventListener('pointermove', event => {
    if (drag.node) { if (Math.abs(event.clientX - drag.sx) + Math.abs(event.clientY - drag.sy) > 4) drag.moved = true; drag.node.x = (event.clientX - transform.x) / transform.k; drag.node.y = (event.clientY - transform.y) / transform.k; drag.node.vx = 0; drag.node.vy = 0; tick(); return; }
    if (pan) { transform.x = pan.ox + event.clientX - pan.x; transform.y = pan.oy + event.clientY - pan.y; applyTransform(); }
  });
  svg.addEventListener('pointerup', () => { if (drag.node && !drag.moved) { const n = drag.node; selectNode(n, elById.get(n.id), liById.get(n.id)); } drag.node = null; pan = null; });
  svg.addEventListener('wheel', event => {
    event.preventDefault();
    autoFit = false;
    const next = Math.min(9, Math.max(.12, transform.k * (event.deltaY > 0 ? .9 : 1.1)));
    transform.x = event.clientX - (event.clientX - transform.x) * (next / transform.k);
    transform.y = event.clientY - (event.clientY - transform.y) * (next / transform.k);
    transform.k = next; applyTransform();
  }, {passive:false});
  function simulate() {
    for (let i = 0; i < nodes.length; i++) for (let j = i + 1; j < nodes.length; j++) {
      const a = nodes[i], b = nodes[j], dx = b.x-a.x || .01, dy = b.y-a.y || .01, d2 = dx*dx + dy*dy, f = Math.min(900 / d2, .05);
      a.vx -= dx*f; a.vy -= dy*f; b.vx += dx*f; b.vy += dy*f;
    }
    links.forEach(({source, target}) => {
      const dx = target.x-source.x, dy = target.y-source.y, dist = Math.hypot(dx,dy) || 1, f = (dist-95)*.006;
      source.vx += dx/dist*f; source.vy += dy/dist*f; target.vx -= dx/dist*f; target.vy -= dy/dist*f;
    });
    nodes.forEach(node => { node.vx += (width()/2-node.x)*.012; node.vy += (height()/2-node.y)*.012; node.vx *= .86; node.vy *= .86; if (drag.node !== node) { node.x += node.vx; node.y += node.vy; }});
  }
  function tick() {
    edgeEls.forEach((path, i) => {
      const { source, target } = links[i];
      path.style.display = (visibleIds.has(source.id) && visibleIds.has(target.id)) ? '' : 'none';
      const dx = target.x - source.x, dy = target.y - source.y;
      const cx = (source.x + target.x) / 2 - dy * 0.16;
      const cy = (source.y + target.y) / 2 + dx * 0.16;
      path.setAttribute('d', 'M' + source.x + ' ' + source.y + ' Q' + cx + ' ' + cy + ' ' + target.x + ' ' + target.y);
    });
    nodeEls.forEach((group, i) => { group.style.display = visibleIds.has(nodes[i].id) ? '' : 'none'; group.setAttribute('transform', 'translate(' + nodes[i].x + ' ' + nodes[i].y + ')'); });
  }
  const indexEl = document.getElementById('index');
  const searchEl = document.getElementById('search');
  const diffPanel = document.getElementById('diff');
  const diffTitle = document.getElementById('diff-title');
  const diffChurn = document.getElementById('diff-churn');
  const diffBody = document.getElementById('diff-body');
  document.getElementById('diff-close').addEventListener('click', () => { diffPanel.hidden = true; });
  window.addEventListener('keydown', event => { if (event.key === 'Escape') diffPanel.hidden = true; });
  const escapeHtml = s => s.replace(/[&<>]/g, c => c === '&' ? '&amp;' : c === '<' ? '&lt;' : '&gt;');
  const KW = {
    go: ['func','package','import','return','if','else','for','range','type','struct','interface','map','chan','go','defer','var','const','switch','case','default','break','continue','fallthrough','select','goto','nil','true','false'],
    js: ['function','return','if','else','for','while','do','const','let','var','class','extends','new','this','import','export','from','default','async','await','yield','try','catch','finally','throw','switch','case','break','continue','typeof','instanceof','in','of','delete','void','null','undefined','true','false'],
    py: ['def','return','if','elif','else','for','while','class','import','from','as','with','try','except','finally','raise','lambda','yield','pass','break','continue','and','or','not','in','is','None','True','False','self','async','await'],
    json: ['true','false','null'],
    def: ['function','return','if','else','for','while','class','import','export','const','let','var','def','func','type','true','false','null','nil','new','public','private','static','void']
  };
  const LANG = {js:'js',ts:'js',tsx:'js',jsx:'js',mjs:'js',cjs:'js',go:'go',py:'py',rb:'py',sh:'py',bash:'py',yml:'py',yaml:'py',toml:'py',json:'json',md:'md'};
  const HASH = new Set(['py','rb','sh','bash','yml','yaml','toml']);
  function specFor(name) {
    const ext = (name.split('.').pop() || '').toLowerCase();
    const lang = LANG[ext] || 'def';
    if (lang === 'md') return {plain: true};
    const kw = KW[lang] || KW.def;
    const str = '"(?:\\\\.|[^"\\\\])*"|\'(?:\\\\.|[^\'\\\\])*\'|\x60(?:\\\\.|[^\x60\\\\])*\x60';
    let com = '(?!)';
    if (HASH.has(ext)) com = '#[^\\n]*';
    else if (lang !== 'json') com = '\\/\\/[^\\n]*|\\/\\*[\\s\\S]*?\\*\\/';
    const num = '\\b\\d[\\d_]*(?:\\.\\d+)?\\b';
    const kwp = kw.length ? '\\b(?:' + kw.join('|') + ')\\b' : '(?!)';
    return {re: new RegExp('(' + str + ')|(' + com + ')|(' + num + ')|(' + kwp + ')', 'g')};
  }
  function highlight(code, spec) {
    if (spec.plain) return escapeHtml(code);
    let out = '', last = 0, m;
    spec.re.lastIndex = 0;
    while ((m = spec.re.exec(code))) {
      if (m[0] === '') { spec.re.lastIndex++; continue; }
      out += escapeHtml(code.slice(last, m.index));
      const cls = m[1] != null ? 't-str' : m[2] != null ? 't-com' : m[3] != null ? 't-num' : 't-kw';
      out += '<span class="' + cls + '">' + escapeHtml(m[0]) + '</span>';
      last = m.index + m[0].length;
    }
    return out + escapeHtml(code.slice(last));
  }
  function showDiff(node) {
    diffTitle.textContent = node.id;
    diffChurn.innerHTML = (node.status === 'context') ? '' : '<b class="pl">+' + (node.added || 0) + '</b><b class="mi">−' + (node.deleted || 0) + '</b>';
    diffBody.innerHTML = '';
    const text = graph.diffs && graph.diffs[node.id];
    diffPanel.hidden = false;
    if (!text) {
      const div = document.createElement('div');
      div.className = 'dl meta';
      div.textContent = node.status === 'context' ? 'Unchanged import target — no diff.' : 'No diff available.';
      diffBody.appendChild(div);
      return;
    }
    const spec = specFor(node.id);
    for (const line of text.split('\n')) {
      const div = document.createElement('div');
      const c = line.charAt(0);
      let kind = '';
      if (line.startsWith('+++') || line.startsWith('---') || line.startsWith('diff ') || line.startsWith('index ') || line.startsWith('new file:') || line.startsWith('rename ') || line.startsWith('similarity ')) kind = 'meta';
      else if (c === '@') kind = 'hunk';
      else if (c === '+') kind = 'add';
      else if (c === '-') kind = 'del';
      div.className = 'dl ' + kind;
      if (kind === 'meta' || kind === 'hunk') {
        div.textContent = line;
      } else {
        const content = (kind === 'add' || kind === 'del') ? line.slice(1) : line;
        div.innerHTML = highlight(content, spec);
      }
      diffBody.appendChild(div);
    }
    diffBody.scrollTop = 0;
  }
  let activeLi = null, activeEl = null, activeNode = null;
  function selectNode(node, el, li) {
    if (activeEl) activeEl.classList.remove('selected');
    if (activeLi) activeLi.classList.remove('active');
    activeEl = el; activeLi = li; activeNode = node;
    el.classList.remove('selected'); void el.offsetWidth; el.classList.add('selected');
    if (li) { li.classList.add('active'); li.scrollIntoView({block: 'nearest'}); }
    autoFit = false;
    transform.k = Math.max(transform.k, 1.9);
    transform.x = (LEFT + (width() - LEFT) / 2) - node.x * transform.k;
    transform.y = height() / 2 - node.y * transform.k;
    applyTransform();
    showDiff(node);
  }
  [...nodes].filter(node => node.status !== 'context').sort((a, b) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0).forEach(node => {
    const el = nodeEls[nodes.indexOf(node)];
    const li = document.createElement('li');
    li.innerHTML = '<i class="dot ' + (node.status || 'context') + '"></i><span></span><em class="ch"><b class="pl"></b><b class="mi"></b></em>';
    li.querySelector('span').textContent = node.id;
    li.querySelector('.pl').textContent = '+' + (node.added || 0);
    li.querySelector('.mi').textContent = '−' + (node.deleted || 0);
    li.title = node.id;
    li.addEventListener('click', () => selectNode(node, el, li));
    liById.set(node.id, li);
    indexEl.appendChild(li);
  });
  function applyFilters() {
    recomputeVisibleIds();
    const q = searchEl.value.trim().toLowerCase();
    for (const node of nodes) {
      const li = liById.get(node.id);
      if (!li) continue;
      const typeHidden = !changedVisible(node);
      const searchHidden = q !== '' && !node.id.toLowerCase().includes(q);
      li.hidden = typeHidden || searchHidden;
    }
    if (activeNode && !visibleIds.has(activeNode.id)) {
      if (activeLi) activeLi.classList.remove('active');
      activeEl.classList.remove('selected');
      activeEl = null; activeLi = null; activeNode = null; diffPanel.hidden = true;
    }
    tick();
    if (autoFit) fitView();
  }
  searchEl.addEventListener('input', applyFilters);
  applyFilters();
  if (graph.changelog && graph.changelog.length) {
    const tabsEl = document.getElementById('tabs');
    const changelogEl = document.getElementById('changelog');
    for (const group of graph.changelog) {
      const head = document.createElement('div');
      head.className = 'cl-group';
      head.textContent = group.title + ' (' + group.commits.length + ')';
      changelogEl.appendChild(head);
      for (const c of group.commits) {
        const row = document.createElement('div');
        row.className = 'cl-commit';
        row.innerHTML = '<span class="cl-scope"></span><span class="cl-subj"></span><span class="cl-hash"></span>';
        if (c.scope) row.querySelector('.cl-scope').textContent = c.scope;
        row.querySelector('.cl-subj').textContent = c.subject;
        row.querySelector('.cl-hash').textContent = (c.hash || '').slice(0, 7);
        changelogEl.appendChild(row);
      }
    }
    tabsEl.hidden = false;
    tabsEl.addEventListener('click', event => {
      const tab = event.target.getAttribute('data-tab');
      if (!tab) return;
      const showChangelog = tab === 'changelog';
      changelogEl.hidden = !showChangelog;
      indexEl.hidden = showChangelog;
      searchEl.hidden = showChangelog;
      tabsEl.querySelectorAll('button').forEach(b => b.classList.toggle('active', b.getAttribute('data-tab') === tab));
    });
  }
  applyTransform();
  let frames = 0;
  function animate(){ if (frames++ < 420 && !drag.node) simulate(); tick(); if (autoFit) fitView(); requestAnimationFrame(animate); }
  animate();
})();
</script>
</body>
</html>
`))
