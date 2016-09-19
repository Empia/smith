# Smith

**Smith** is a Kubernetes workflow engine / resource manager **prototype**.
It's not complete yet, it's still under active development.
It may or may not fulfil the requirements of https://github.com/kubernetes/kubernetes/issues/1704.

## The idea

What if we build a service that allows us to manage Kubernetes' built-in resources and other
[Third Party Resources](https://github.com/kubernetes/kubernetes/blob/master/docs/design/extending-api.md)
(TPR) in a generic way? Similar to how AWS CloudFormation (or Google Deployment Manager) allows us to manage any
AWS/GCE and custom resource. Then we could expose all the resources we need
to integrate as Third Party Resources and manage them declaratively. This is an open architecture
with Kubernetes as its core. Other microservices can create/update/watch TPRs to co-ordinate their work/lifecycle.

## Implementation

A group of resources is defined using a Template (just like a Stack for AWS CloudFormation).
The Template itself is also a Kubernetes TPR.
Smith watches for new instances of a Template (and events to existing ones), picks them up and processes them.

Processing involves parsing the template, building a dependency graph (which is implicitly defined in the template),
walking the graph, and creating/updating necessary resources. Each created/referenced resource gets
an annotation/label pointing at the Template.

### Outputs
Some resource types can have Outputs - values that are returned by a TPR upon creation.
For example a TPR may define a DB to be created. The Outputs will be a Secret
that contains the DB password, a plain string with a username, the DB URL and so on.
A resource may consume another resources Output by referencing it in its template.

### Dependencies
Resources may depend on each other explicitly via DependsOn object references or implicitly
via references to other resources' outputs. Resources should be created in the reverse dependency order.

### States
READY is the state of a Resource when it can be considered created. E.g. if it is
a DB then it means it was provisioned and set up as requested. State is part of
[Status](https://github.com/kubernetes/kubernetes/blob/master/docs/devel/api-conventions.md#spec-and-status).

### Event-driven and stateless
Smith does not block while waiting for a resource to reach the READY state. Instead, when walking the dependency
graph, if a resource is not in the READY state (still being created) it stops processing the
template. Full template re-processing is triggered by events about the watched resources. Smith is
watching all supported resource types and inspects annotations/labels on events to find out which
template should be re-processed because of the event. This should scale better than watching
individual resources and much better than polling individual resources.

## Notes

### On [App Controller](https://github.com/Mirantis/k8s-AppController)
Mirantis App Controller (discussed here https://github.com/kubernetes/kubernetes/issues/29453) is a very similar workflow engine with a few differences.

1. Presumably it works :)
2. Graph of dependencies is defined explicitly. IMHO this is not the best user experience.
3. It uses polling and blocks while waiting for the resource to become READY.
4. The goal of Smith is to manage instances of TPRs. App Controller cannot manage them yet.

It is not better or worse, just different set of design choices.