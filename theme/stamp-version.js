// USWDS's own build stamps a version banner comment into the compiled CSS
// (/*! uswds @version */ -> /*! uswds vX.Y.Z */) via a gulp-replace step in
// its own tasks/sass.js -- see github.com/uswds/uswds/blob/main/tasks/sass.js.
// This project compiles with the plain `sass` CLI, not USWDS's Gulp task, so
// that substitution never runs and the placeholder ships as-is. That banner
// comment is also the *only* thing GSA's site-scanning-engine looks at to
// detect a site's USWDS version (a regex over delivered CSS, see
// libs/core-scanner/src/scans/uswds.ts in github.com/GSA/site-scanning-engine)
// -- so leaving it unstamped means this project's own report would show up
// as "unknown version" in that data if GSA ever scanned it. Replicate just
// that one substitution here rather than pulling in gulp + its plugin chain.
const fs = require("fs");
const path = require("path");

const cssPath = path.join(__dirname, "..", "docs", "assets", "uswds", "css", "uswds.css");
// Read the package.json file directly rather than `require("@uswds/uswds/package.json")`
// -- its own "exports" map doesn't expose that subpath, so the require would throw.
const pkgPath = path.join(__dirname, "..", "node_modules", "@uswds", "uswds", "package.json");
const uswdsVersion = JSON.parse(fs.readFileSync(pkgPath, "utf8")).version;

const css = fs.readFileSync(cssPath, "utf8");
const stamped = css.replace(/\buswds @version\b/g, `uswds v${uswdsVersion}`);

if (stamped === css) {
  console.error(`warning: no "uswds @version" placeholder found in ${cssPath} -- already stamped, or theme/styles.scss changed`);
} else {
  fs.writeFileSync(cssPath, stamped);
  console.log(`stamped uswds v${uswdsVersion} into ${cssPath}`);
}
