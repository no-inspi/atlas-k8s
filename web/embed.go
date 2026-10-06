// Package web embarque le front compilé (web/dist) dans le binaire.
package web

import "embed"

// Dist contient le résultat de `vite build`. Il peut ne contenir que .gitkeep
// quand le front n'a pas été compilé (tests Go seuls).
//
//go:embed all:dist
var Dist embed.FS
