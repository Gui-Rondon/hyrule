import React, {useEffect, useState} from 'react';
import {useLocation} from '@docusaurus/router';
import styles from './styles.module.css';

// Only on /docs/ routes (both the Learn and Reference sidebars live
// under that one routeBasePath) — not the homepage or anywhere else.
// `includes`, not `startsWith`: during static generation pathname may
// or may not carry the site's baseUrl prefix ('/hyrule/docs/...' vs
// '/docs/...') depending on render pass — checked empirically, not
// assumed, after the stricter prefix check rendered the bar nowhere.
const DOCS_PATH_SEGMENT = '/docs/';

export default function ReadingProgress() {
  const {pathname} = useLocation();
  const isDocsPage = pathname.includes(DOCS_PATH_SEGMENT);
  const [progress, setProgress] = useState(0);

  useEffect(() => {
    if (!isDocsPage) {
      return undefined;
    }

    function updateProgress() {
      const {scrollTop, scrollHeight, clientHeight} = document.documentElement;
      const scrollable = scrollHeight - clientHeight;
      setProgress(scrollable > 0 ? Math.min(100, (scrollTop / scrollable) * 100) : 0);
    }

    updateProgress();
    window.addEventListener('scroll', updateProgress, {passive: true});
    window.addEventListener('resize', updateProgress);
    return () => {
      window.removeEventListener('scroll', updateProgress);
      window.removeEventListener('resize', updateProgress);
    };
    // pathname: recompute (and effectively reset) on every doc-to-doc
    // navigation, since this component stays mounted across route
    // changes rather than remounting.
  }, [isDocsPage, pathname]);

  if (!isDocsPage) {
    return null;
  }

  return (
    <div className={styles.track} aria-hidden="true">
      <div className={styles.fill} style={{width: `${progress}%`}} />
    </div>
  );
}
