// D3-based charts for the USWDS Adoption Pulse report. Hand-written, checked
// into the repo (unlike docs/index.html, this doesn't change per report run)
// -- scripts/build_report.py only emits the data + a call to these functions.
//
// Both chart types get real axes (D3's tick generators, not hand-picked
// pixel coordinates), a y-axis title, hairline recessive gridlines, and a
// hover layer (crosshair+tooltip for lines, per-bar tooltip for bars) per
// the project's dataviz guidelines. Every value shown on hover is also
// visible without it, in the legend below each chart -- hover enhances, it
// never gates.
(function () {
  const MARGIN = { top: 18, right: 24, bottom: 28, left: 60 };
  const WIDTH = 920;
  const HEIGHT = 300;
  const INNER_W = WIDTH - MARGIN.left - MARGIN.right;
  const INNER_H = HEIGHT - MARGIN.top - MARGIN.bottom;

  const parseDate = d3.timeParse("%Y-%m-%d");

  function makeTooltip(root) {
    return d3.select(root).append("div").attr("class", "report-tooltip").style("opacity", 0);
  }

  function setTooltipContent(tooltip, title, rows) {
    const node = tooltip.node();
    node.textContent = "";
    const titleEl = document.createElement("div");
    titleEl.className = "report-tooltip-title";
    titleEl.textContent = title;
    node.appendChild(titleEl);
    rows.forEach((r) => {
      const rowEl = document.createElement("div");
      rowEl.className = "report-tooltip-row";
      const swatch = document.createElement("span");
      swatch.className = "report-tooltip-swatch";
      swatch.style.background = r.color;
      const label = document.createElement("span");
      label.className = "report-tooltip-label";
      label.textContent = r.label;
      const value = document.createElement("strong");
      value.className = "report-tooltip-value";
      value.textContent = r.value;
      rowEl.append(swatch, label, value);
      node.appendChild(rowEl);
    });
  }

  function positionTooltip(tooltip, root, px, py) {
    const rootRect = root.getBoundingClientRect();
    const scale = rootRect.width / WIDTH;
    tooltip
      .style("opacity", 1)
      .style("left", px * scale + 12 + "px")
      .style("top", py * scale + "px");
  }

  function addYAxisTitle(svg, label) {
    svg
      .append("text")
      .attr("class", "report-axis-title")
      .attr("transform", "rotate(-90)")
      .attr("x", -(MARGIN.top + INNER_H / 2))
      .attr("y", 16)
      .attr("text-anchor", "middle")
      .text(label);
  }

  function drawLineChart(selector, cfg) {
    const root = document.querySelector(selector);
    if (!root || !cfg.dates.length) return;
    root.innerHTML = "";

    const dates = cfg.dates.map(parseDate);
    const svg = d3
      .select(root)
      .append("svg")
      .attr("viewBox", `0 0 ${WIDTH} ${HEIGHT}`)
      .attr("role", "img")
      .attr("aria-label", cfg.ariaLabel || "");
    const g = svg.append("g").attr("transform", `translate(${MARGIN.left},${MARGIN.top})`);

    const x = d3.scaleTime().domain(d3.extent(dates)).range([0, INNER_W]);
    const yTop = cfg.yDomain ? cfg.yDomain[1] : d3.max(cfg.series.flatMap((s) => s.values)) * 1.12;
    const yBottom = cfg.yDomain ? cfg.yDomain[0] : 0;
    const y = d3.scaleLinear().domain([yBottom, yTop]).nice().range([INNER_H, 0]);

    g.append("g")
      .attr("class", "report-grid")
      .call(d3.axisLeft(y).tickSize(-INNER_W).tickFormat("").ticks(5));

    g.append("g")
      .attr("class", "report-axis")
      .attr("transform", `translate(0,${INNER_H})`)
      .call(d3.axisBottom(x).ticks(6));

    g.append("g").attr("class", "report-axis").call(d3.axisLeft(y).ticks(5).tickFormat(cfg.yTickFormat ? d3.format(cfg.yTickFormat) : null));

    addYAxisTitle(svg, cfg.yLabel);

    if (cfg.annotation) {
      const ax = x(parseDate(cfg.annotation.date));
      g.append("line").attr("class", "report-annotation-line").attr("x1", ax).attr("x2", ax).attr("y1", 0).attr("y2", INNER_H);
      g.append("text").attr("class", "report-annotation-label").attr("x", ax + 6).attr("y", 12).text(cfg.annotation.label);
    }

    const line = d3.line().x((d, i) => x(dates[i])).y((d) => y(d));

    cfg.series.forEach((s) => {
      g.append("path").datum(s.values).attr("class", "report-line").attr("fill", "none").attr("stroke", s.color).attr("d", line);
      const lastIdx = s.values.length - 1;
      g.append("circle")
        .attr("class", "report-marker")
        .attr("cx", x(dates[lastIdx]))
        .attr("cy", y(s.values[lastIdx]))
        .attr("fill", s.color);
    });

    // Hover: a crosshair on the nearest date, one tooltip listing every series.
    const tooltip = makeTooltip(root);
    const crosshair = g.append("line").attr("class", "report-crosshair").attr("y1", 0).attr("y2", INNER_H).style("opacity", 0);
    const fmtDate = d3.timeFormat("%b %-d, %Y");
    const fmtVal = d3.format(cfg.yTickFormat || ",");

    function onMove(event) {
      const [mx] = d3.pointer(event, this);
      const x0 = x.invert(mx);
      const idx = Math.max(0, Math.min(dates.length - 1, d3.bisector((d) => d).center(dates, x0)));
      const px = x(dates[idx]);
      crosshair.attr("x1", px).attr("x2", px).style("opacity", 1);
      const rows = cfg.series.map((s) => ({ color: s.color, label: s.label, value: fmtVal(s.values[idx]) }));
      setTooltipContent(tooltip, fmtDate(dates[idx]), rows);
      const topY = d3.min(cfg.series.map((s) => y(s.values[idx])));
      positionTooltip(tooltip, root, px + MARGIN.left, topY + MARGIN.top);
    }
    function onLeave() {
      crosshair.style("opacity", 0);
      tooltip.style("opacity", 0);
    }

    g.append("rect")
      .attr("class", "report-overlay")
      .attr("width", INNER_W)
      .attr("height", INNER_H)
      .attr("fill", "transparent")
      .on("mousemove", onMove)
      .on("touchmove", onMove)
      .on("mouseleave", onLeave);
  }

  function drawBarChart(selector, cfg) {
    const root = document.querySelector(selector);
    if (!root || !cfg.categories.length) return;
    root.innerHTML = "";

    const svg = d3
      .select(root)
      .append("svg")
      .attr("viewBox", `0 0 ${WIDTH} ${HEIGHT}`)
      .attr("role", "img")
      .attr("aria-label", cfg.ariaLabel || "");
    const g = svg.append("g").attr("transform", `translate(${MARGIN.left},${MARGIN.top})`);

    const x = d3.scaleBand().domain(cfg.categories).range([0, INNER_W]).padding(0.4);
    const y = d3.scaleLinear().domain([0, d3.max(cfg.values) * 1.2]).nice().range([INNER_H, 0]);
    const fmtVal = d3.format(cfg.valueFormat || ",");

    g.append("g")
      .attr("class", "report-grid")
      .call(d3.axisLeft(y).tickSize(-INNER_W).tickFormat("").ticks(5));

    g.append("g").attr("class", "report-axis").attr("transform", `translate(0,${INNER_H})`).call(d3.axisBottom(x));
    g.append("g").attr("class", "report-axis").call(d3.axisLeft(y).ticks(5));

    addYAxisTitle(svg, cfg.yLabel);

    const tooltip = makeTooltip(root);
    const data = cfg.categories.map((c, i) => ({ cat: c, value: cfg.values[i], color: cfg.colors[i] }));

    g.selectAll(".report-bar")
      .data(data)
      .join("rect")
      .attr("class", "report-bar")
      .attr("x", (d) => x(d.cat))
      .attr("width", x.bandwidth())
      .attr("y", (d) => y(d.value))
      .attr("height", (d) => INNER_H - y(d.value))
      .attr("rx", 2)
      .attr("fill", (d) => d.color)
      .on("mousemove", function (event, d) {
        setTooltipContent(tooltip, d.cat, [{ color: d.color, label: cfg.yLabel, value: fmtVal(d.value) }]);
        positionTooltip(tooltip, root, x(d.cat) + x.bandwidth() / 2 + MARGIN.left, y(d.value) + MARGIN.top);
        d3.select(this).classed("is-hovered", true);
      })
      .on("mouseleave", function () {
        tooltip.style("opacity", 0);
        d3.select(this).classed("is-hovered", false);
      });

    g.selectAll(".report-bar-label")
      .data(data)
      .join("text")
      .attr("class", "report-bar-label")
      .attr("x", (d) => x(d.cat) + x.bandwidth() / 2)
      .attr("y", (d) => y(d.value) - 8)
      .attr("text-anchor", "middle")
      .text((d) => fmtVal(d.value));
  }

  window.reportCharts = { drawLineChart, drawBarChart };
})();
