import React from 'react';
import ReadingProgress from '@site/src/components/ReadingProgress';

// Docusaurus's own site-wide extension point (not a swizzle of any
// particular theme component) — renders the reading-progress bar
// alongside the app on every route; ReadingProgress itself decides
// whether to render anything based on the current path.
export default function Root({children}) {
  return (
    <>
      {children}
      <ReadingProgress />
    </>
  );
}
