# Building Block View

A fixture with deliberate violations for the architecture test.

## Alpha

Alpha offers one interface.

```arc42
:::building-block
id: bb-alpha
title: Alpha
path: alpha
:::
```

### Alpha interface

The interface of alpha.

```arc42
:::interface
id: if-alpha
title: Alpha interface
provider: bb-alpha
path: alpha/api
:::
```

## Beta

Beta requires alpha but uses its inside instead.

```arc42
:::building-block
id: bb-beta
title: Beta
path: beta
requires: if-alpha
:::
```

### Beta interface

Its path lies outside its provider.

```arc42
:::interface
id: if-beta
title: Beta interface
provider: bb-beta
path: gamma
:::
```

## Gamma

Gamma uses alpha without requiring it.

```arc42
:::building-block
id: bb-gamma
title: Gamma
path: gamma
:::
```

## Delta

Delta has no path.

```arc42
:::building-block
id: bb-delta
title: Delta
:::
```
