declare module 'd3-force-3d' {
  export function forceSimulation(nodes?: any[], dimensions?: 1 | 2 | 3): any
  export function forceLink(links?: any[]): any
  export function forceManyBody(): any
  export function forceCenter(x?: number, y?: number, z?: number): any
  export function forceCollide(radius?: (node: any) => number): any
  export function forceX(x?: number): any
  export function forceY(y?: number): any
}
