(function () {
  var LOW_CONFIDENCE = 0.7;
  function renderNode() {
    var container = document.getElementById("node-cy");
    if (!container || !window.cytoscape) return;
    var nodeID = container.getAttribute("data-node-id");
    if (!nodeID) return;
    var params = new URLSearchParams({ id: nodeID, limit: "100" });
    fetch("/graph/node?" + params.toString())
      .then(function (r) { return r.json(); })
      .then(function (data) {
        var nodes = [];
        var edges = [];
        var seen = {};
        seen[nodeID] = true;
        nodes.push({ group: "nodes", data: {
          id: nodeID, label: data.document && data.document.name || nodeID,
          type: data.document && data.document.type || "", degree: data.total_neighbors || 0
        }});
        (data.edges || []).forEach(function (group) {
          (group.edges || []).forEach(function (edge) {
            if (!seen[edge.neighbor_id]) {
              seen[edge.neighbor_id] = true;
              nodes.push({ group: "nodes", data: {
                id: edge.neighbor_id, label: edge.neighbor_name,
                type: edge.neighbor_type || "", degree: 1
              }});
            }
            edges.push({ group: "edges", data: {
              id: edge.id, source: edge.source, target: edge.target,
              type: edge.type, dashed: edge.confidence !== undefined && edge.confidence < LOW_CONFIDENCE
            }});
          });
        });
        (data.inferred || []).forEach(function (inf) {
          if (!seen[inf.entity_id]) {
            seen[inf.entity_id] = true;
            nodes.push({ group: "nodes", data: {
              id: inf.entity_id, label: inf.entity_name,
              type: inf.entity_type || "", degree: 0
            }});
          }
          edges.push({ group: "edges", data: {
            id: "inferred-" + inf.entity_id, source: nodeID, target: inf.entity_id,
            type: inf.type, dashed: true
          }});
        });
        cytoscape({
          container: container,
          elements: { nodes: nodes, edges: edges },
          style: [
            { selector: "node", style: {
              "background-color": "#4f8cff",
              "label": "data(label)",
              "font-size": 10,
              "color": "#cfe0ff",
              "text-valign": "bottom",
              "text-margin-y": 4,
              "width": "mapData(degree, 0, 20, 14, 36)",
              "height": "mapData(degree, 0, 20, 14, 36)"
            }},
            { selector: "edge", style: {
              "width": 1,
              "line-color": "#3a4a6b",
              "target-arrow-color": "#3a4a6b",
              "target-arrow-shape": "triangle",
              "curve-style": "bezier",
              "label": "data(type)",
              "font-size": 8,
              "color": "#8fa0bd"
            }},
            { selector: "edge[dashed = true]", style: {
              "line-style": "dashed",
              "line-color": "#e5a63b",
              "target-arrow-color": "#e5a63b"
            }}
          ]
        }).layout({ name: "cose", animate: false }).run();
      });
  }
  function onReady(fn) {
    if (document.readyState === "loading") {
      document.addEventListener("DOMContentLoaded", fn);
    } else {
      fn();
    }
  }
  onReady(function () {
    document.addEventListener("htmx:afterSwap", function (evt) {
      if (evt.detail && evt.detail.target && evt.detail.target.id === "entity-panel") renderNode();
    });
    window.kbNodeViewRender = renderNode;
  });
})();
