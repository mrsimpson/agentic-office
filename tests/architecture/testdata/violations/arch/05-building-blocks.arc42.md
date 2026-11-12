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

### Alpha twin interface

A second interface on the path of the alpha interface.

```arc42
:::interface
id: if-alpha-twin
title: Alpha twin interface
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

Its path lies outside its provider, on the path of gamma.

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
requires: if-omega
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

## Omega

Omega is a parent. Its interface crosses its boundary and is realized by its child one.

```arc42
:::building-block
id: bb-omega
title: Omega
path: omega
:::
```

### Omega interface

Realized by one.

```arc42
:::interface
id: if-omega
title: Omega interface
provider: bb-omega
path: omega/one/api
x-realized-by: bb-one
:::
```

### Bad interface

Names a realizer that is not a child of omega. It shares the path of omega.

```arc42
:::interface
id: if-bad
title: Bad interface
provider: bb-omega
path: omega
x-realized-by: bb-alpha
:::
```

### One

Realizes the omega interface.

```arc42
:::building-block
id: bb-one
title: One
parent: bb-omega
path: omega/one
:::
```

### Two

Uses the omega interface without requiring it.

```arc42
:::building-block
id: bb-two
title: Two
parent: bb-omega
path: omega/two
:::
```
