"""Bounded ticks for service managers; persisted cycles survive process death."""


class Scheduler:
    def __init__(self,cycle,identity):
        self.cycle,self.identity = cycle,identity
    def tick(self):
        result = self.cycle.tick(self.identity)
        self.cycle.dispatch(self.identity)
        return result
