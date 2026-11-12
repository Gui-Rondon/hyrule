import React from 'react';
// Wrapping (not swizzle --eject): @theme-original keeps using Docusaurus's
// own Copyright implementation untouched, we just add a sibling before it.
// This is the one place in the project that needed any component
// override at all, since the pure-CSS Triforce needs a real element to
// anchor its own ::before/::after (a pseudo-element can't itself host
// nested pseudo-elements, so attaching it to .footer__copyright::before
// directly wasn't possible with 3 triangles).
import Copyright from '@theme-original/Footer/Copyright';
import styles from './styles.module.css';

export default function CopyrightWrapper(props) {
  return (
    <>
      <span className={styles.triforce} aria-hidden="true" />
      <Copyright {...props} />
    </>
  );
}
